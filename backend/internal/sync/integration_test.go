package sync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	grpctransport "github.com/cu3rd/yc-hack-horses/backend/internal/transport/grpc"

	syncv1 "github.com/cu3rd/yc-hack-horses/backend/api/gen/sync/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// node — нода поверх in-memory gRPC (bufconn) с fake-хранилищами.
type node struct {
	engine *syncengine.Engine
	events *fakeEventStore
	blobs  *fakeBlobStore
	client *grpctransport.Client
}

func newNode(t *testing.T, id string) *node {
	t.Helper()
	events := newFakeEventStore()
	blobs := newFakeBlobStore()
	identity := syncengine.NodeIdentity{NodeID: id, Role: syncengine.RoleLocal, AppVersion: "test-1.0"}
	grpcServer := grpc.NewServer()
	grpctransport.Register(grpcServer, grpctransport.NewServer(grpctransport.ServerDeps{
		Events:   events,
		Blobs:    blobs,
		Identity: identity,
	}))
	listen := bufconn.Listen(1 << 20)
	go func() { _ = grpcServer.Serve(listen) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listen.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := grpctransport.NewClient(syncv1.NewSyncServiceClient(conn), nil)
	engine := syncengine.NewEngine(events, blobs, identity,
		syncengine.Config{BatchLimit: 3, ChunkSize: 1024}, slog.Default())
	return &node{engine: engine, events: events, blobs: blobs, client: client}
}

func makeEvent(nodeID string, seq uint64, payload string) domain.Event {
	return domain.Event{
		EventID:     fmt.Sprintf("evt-%s-%d", nodeID, seq),
		NodeID:      nodeID,
		Sequence:    seq,
		EventType:   domain.EventObservationAdded,
		AggregateID: fmt.Sprintf("obs-%d", seq),
		OccurredAt:  time.Now().UTC().Truncate(time.Millisecond),
		Payload:     []byte(fmt.Sprintf(`{"n":%q,"seq":%d}`, payload, seq)),
	}
}

// TestSyncBidirectionalNoDuplicates — события A доезжают до B, повторный sync
// ничего не дублирует.
func TestSyncBidirectionalNoDuplicates(t *testing.T) {
	a := newNode(t, "node-a")
	b := newNode(t, "node-b")

	var events []domain.Event
	for i := 1; i <= 10; i++ {
		events = append(events, makeEvent("node-a", uint64(i), "obs"))
	}
	if err := a.events.Append(context.Background(), events); err != nil {
		t.Fatalf("seed events: %v", err)
	}

	pulled, pushed, err := a.engine.Sync(context.Background(), b.client)
	if err != nil {
		t.Fatalf("sync a->b: %v", err)
	}
	if pulled != 0 || pushed != 10 {
		t.Fatalf("a->b: want pushed=10, got pulled=%d pushed=%d", pulled, pushed)
	}
	if got := b.events.count("node-a"); got != 10 {
		t.Fatalf("node-b has %d events from node-a, want 10", got)
	}

	pulled, pushed, err = a.engine.Sync(context.Background(), b.client)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if pulled != 0 || pushed != 0 {
		t.Fatalf("resync moved events: pulled=%d pushed=%d, want 0/0", pulled, pushed)
	}
	if got := b.events.count("node-a"); got != 10 {
		t.Fatalf("resync duplicated: node-b has %d events, want 10", got)
	}

	// Симметрично: B создаёт свои события, они доезжают до A.
	var bevents []domain.Event
	for i := 1; i <= 3; i++ {
		bevents = append(bevents, makeEvent("node-b", uint64(i), "obs-b"))
	}
	if err := b.events.Append(context.Background(), bevents); err != nil {
		t.Fatalf("seed b events: %v", err)
	}
	pulled, pushed, err = a.engine.Sync(context.Background(), b.client)
	if err != nil {
		t.Fatalf("sync b->a: %v", err)
	}
	if pulled != 3 || pushed != 0 {
		t.Fatalf("b->a: want pulled=3, got pulled=%d pushed=%d", pulled, pushed)
	}
	if got := a.events.count("node-b"); got != 3 {
		t.Fatalf("node-a has %d events from node-b, want 3", got)
	}
}

// brokenPushPeer рвёт PushEvents после первого успешного батча — имитация
// обрыва соединения посреди клиентского стрима.
type brokenPushPeer struct {
	inner  syncengine.Peer
	broken bool // true — порвать после первого батча
}

func (p *brokenPushPeer) ExchangeVectorClock(ctx context.Context, ours storage.VectorClock) (storage.VectorClock, error) {
	return p.inner.ExchangeVectorClock(ctx, ours)
}

func (p *brokenPushPeer) PullEvents(ctx context.Context, have storage.VectorClock, limit int, yield func([]domain.Event) error) error {
	return p.inner.PullEvents(ctx, have, limit, yield)
}

func (p *brokenPushPeer) PushEvents(ctx context.Context) (syncengine.PeerEventSink, error) {
	sink, err := p.inner.PushEvents(ctx)
	if err != nil {
		return nil, err
	}
	if !p.broken {
		return sink, nil
	}
	p.broken = false
	return &brokenSink{inner: sink}, nil
}

type brokenSink struct {
	inner syncengine.PeerEventSink
	used  bool
}

func (s *brokenSink) Send(events []domain.Event) error {
	if !s.used {
		s.used = true
		if err := s.inner.Send(events); err != nil {
			return err
		}
		return errors.New("simulated: connection reset mid-push")
	}
	return s.inner.Send(events)
}

func (s *brokenSink) Close() (syncengine.Ack, error) {
	return s.inner.Close()
}

func (p *brokenPushPeer) RequestMissingBlobs(ctx context.Context, hashes []string) ([]string, error) {
	return p.inner.RequestMissingBlobs(ctx, hashes)
}

func (p *brokenPushPeer) UploadBlob(ctx context.Context, sha256hex string, r io.Reader) error {
	return p.inner.UploadBlob(ctx, sha256hex, r)
}

func (p *brokenPushPeer) DownloadBlob(ctx context.Context, sha256hex string) (io.ReadCloser, error) {
	return p.inner.DownloadBlob(ctx, sha256hex)
}

// TestSyncRecoverAfterDisconnect — обрыв посреди PullEvents; повторный sync
// доезжает недостающими без восстановления сессии.
func TestSyncRecoverAfterDisconnect(t *testing.T) {
	a := newNode(t, "node-a")
	b := newNode(t, "node-b")

	var events []domain.Event
	for i := 1; i <= 7; i++ {
		events = append(events, makeEvent("node-a", uint64(i), "obs"))
	}
	if err := a.events.Append(context.Background(), events); err != nil {
		t.Fatalf("seed events: %v", err)
	}

	broken := &brokenPushPeer{inner: b.client, broken: true}
	if _, _, err := a.engine.Sync(context.Background(), broken); err == nil {
		t.Fatal("first sync should fail on simulated disconnect")
	}
	// Первый батч отправлен, но gRPC доставляет его асинхронно — ждём записи.
	partial := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if partial = b.events.count("node-a"); partial > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if partial == 0 || partial >= 7 {
		t.Fatalf("expected partial delivery, got %d/7", partial)
	}

	// Повторный sync с актуальным clock — состояние сессии не нужно.
	if _, _, err := a.engine.Sync(context.Background(), b.client); err != nil {
		t.Fatalf("retry sync: %v", err)
	}
	if got := b.events.count("node-a"); got != 7 {
		t.Fatalf("after retry node-b has %d events, want 7", got)
	}
}

// TestBlobTransferWithHashCheck — чанковая передача blob, проверка sha256,
// повторная передача не дублирует.
func TestBlobTransferWithHashCheck(t *testing.T) {
	a := newNode(t, "node-a")
	b := newNode(t, "node-b")

	data := make([]byte, 5000)
	for i := range data {
		data[i] = byte(i % 251)
	}
	hash := hexHash(data)
	if err := a.blobs.Put(context.Background(), hash, "image/jpeg", data); err != nil {
		t.Fatalf("put blob on a: %v", err)
	}

	missing, err := b.client.RequestMissingBlobs(context.Background(), []string{hash})
	if err != nil {
		t.Fatalf("request missing: %v", err)
	}
	if len(missing) != 1 || missing[0] != hash {
		t.Fatalf("b should miss the blob, got %v", missing)
	}
	if err := b.client.UploadBlob(context.Background(), hash, newChunkedReader(data, 1024)); err != nil {
		t.Fatalf("upload blob: %v", err)
	}
	got, err := b.blobs.Get(context.Background(), hash)
	if err != nil {
		t.Fatalf("get blob on b: %v", err)
	}
	if string(got) != string(data) {
		t.Fatal("blob content mismatch after transfer")
	}

	// Повторная передача: дедупликация, записи больше не происходит.
	before := b.blobs.putCount(hash)
	if err := b.client.UploadBlob(context.Background(), hash, newChunkedReader(data, 1024)); err != nil {
		t.Fatalf("re-upload: %v", err)
	}
	if after := b.blobs.putCount(hash); after != before {
		t.Fatalf("re-upload wrote blob again: puts %d -> %d", before, after)
	}

	// Порча: содержимое не совпадает с заявленным хэшем — отказ без записи.
	err = b.client.UploadBlob(context.Background(), hash, newChunkedReader(append([]byte(nil), data[:len(data)-1]...), 1024))
	if err == nil {
		t.Fatal("upload with hash-mismatched content should fail")
	}

	// Обратное направление: DownloadBlob отдаёт чанки с offset, контент тот же.
	r, err := a.client.DownloadBlob(context.Background(), hash)
	if err != nil {
		t.Fatalf("download blob: %v", err)
	}
	defer r.Close() //nolint:errcheck // тест
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(buf) != string(data) {
		t.Fatal("downloaded content mismatch")
	}
}

// hexHash — sha256-хэш данных в hex.
func hexHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// chunkedReader — io.Reader, отдающий данные чанками (как при передаче blob).
type chunkedReader struct {
	data   []byte
	chunk  int
	offset int
}

func newChunkedReader(data []byte, chunk int) *chunkedReader {
	return &chunkedReader{data: data, chunk: chunk}
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.offset >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(p) {
		n = len(p)
	}
	if c.offset+n > len(c.data) {
		n = len(c.data) - c.offset
	}
	copy(p[:n], c.data[c.offset:c.offset+n])
	c.offset += n
	return n, nil
}
