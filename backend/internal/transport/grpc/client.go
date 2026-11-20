package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	syncv1 "github.com/cu3rd/yc-hack-horses/backend/api/gen/sync/v1"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// Client — адаптер syncengine.Peer поверх сгенерированного gRPC-клиента.
// Нулевая совместимость со старым кодом не требуется: единственный Peer.
var (
	_ syncengine.Peer          = (*Client)(nil)
	_ syncengine.HandshakePeer = (*Client)(nil)
)

// ClientConfig — параметры клиента.
type ClientConfig struct {
	// Timeout — таймаут одного унарного RPC. 0 = 30s; стримы получают 4x.
	Timeout time.Duration
}

// Client — Peer через gRPC.
type Client struct {
	c  syncv1.SyncServiceClient
	cc *ClientConfig
}

// NewClient оборачивает сгенерированный клиент в интерфейс движка.
func NewClient(c syncv1.SyncServiceClient, cfg *ClientConfig) *Client {
	if cfg == nil {
		cfg = &ClientConfig{}
	}
	return &Client{c: c, cc: cfg}
}

func (cl *Client) timeout() time.Duration {
	if cl.cc.Timeout <= 0 {
		return 30 * time.Second
	}
	return cl.cc.Timeout
}

// Handshake — рукопожатие с пиром.
func (cl *Client) Handshake(ctx context.Context, req syncengine.HandshakeRequest) (syncengine.HandshakeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, cl.timeout())
	defer cancel()
	resp, err := cl.c.Handshake(ctx, &syncv1.HandshakeRequest{
		NodeId:     req.NodeID,
		ProtocolVersion: syncengine.ProtocolVersion,
		AppVersion: req.AppVersion,
	})
	if err != nil {
		return syncengine.HandshakeInfo{}, fmt.Errorf("grpc handshake: %w", err)
	}
	return syncengine.HandshakeInfo{
		NodeID:          resp.GetNodeId(),
		ProtocolVersion: resp.GetProtocolVersion(),
		AppVersion:      resp.GetAppVersion(),
		Role:            resp.GetRole(),
	}, nil
}

// ExchangeVectorClock — обмен векторными часами.
func (cl *Client) ExchangeVectorClock(ctx context.Context, ours storage.VectorClock) (storage.VectorClock, error) {
	ctx, cancel := context.WithTimeout(ctx, cl.timeout())
	defer cancel()
	resp, err := cl.c.ExchangeVectorClock(ctx, clockToProto(ours))
	if err != nil {
		return nil, fmt.Errorf("grpc exchange clock: %w", err)
	}
	return clockFromProto(resp), nil
}

// PullEvents — серверный стрим недостающих событий; батчи отдаются через yield.
func (cl *Client) PullEvents(ctx context.Context, have storage.VectorClock, limit int, yield func([]domain.Event) error) error {
	ctx, cancel := context.WithTimeout(ctx, 4*cl.timeout()) // стрим живёт дольше одного RPC
	defer cancel()
	stream, err := cl.c.PullEvents(ctx, &syncv1.PullEventsRequest{Have: clockToProto(have), Limit: int32(limit)})
	if err != nil {
		return fmt.Errorf("grpc pull events: open: %w", err)
	}
	for {
		batch, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("grpc pull events: recv: %w", err)
		}
		events, err := eventsFromProto(batch.GetEvents())
		if err != nil {
			return fmt.Errorf("grpc pull events: decode: %w", err)
		}
		if len(events) == 0 {
			continue
		}
		if err := yield(events); err != nil {
			return err
		}
		if batch.GetFinal() {
			return nil
		}
	}
}

// PushEvents — клиентский стрим батчей с AckBatch в конце.
func (cl *Client) PushEvents(ctx context.Context) (syncengine.PeerEventSink, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*cl.timeout())
	defer cancel()
	stream, err := cl.c.PushEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("grpc push events: open: %w", err)
	}
	return &pushSink{stream: stream}, nil
}

type pushSink struct {
	stream syncv1.SyncService_PushEventsClient
}

func (p *pushSink) Send(events []domain.Event) error {
	batch, err := eventsToProto(events)
	if err != nil {
		return fmt.Errorf("grpc push events: encode: %w", err)
	}
	if err := p.stream.Send(&syncv1.EventBatch{Events: batch}); err != nil {
		return fmt.Errorf("grpc push events: send: %w", err)
	}
	return nil
}

func (p *pushSink) Close() (syncengine.Ack, error) {
	resp, err := p.stream.CloseAndRecv()
	if err != nil {
		return syncengine.Ack{}, fmt.Errorf("grpc push events: ack: %w", err)
	}
	return syncengine.Ack{
		AckedEventIDs: resp.GetAckEventIds(),
		AckedThrough:  resp.GetAckedThrough(),
		NodeID:        resp.GetNodeId(),
	}, nil
}

// RequestMissingBlobs — какие хэши отсутствуют у пира.
func (cl *Client) RequestMissingBlobs(ctx context.Context, hashes []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, cl.timeout())
	defer cancel()
	resp, err := cl.c.RequestMissingBlobs(ctx, &syncv1.BlobHashList{Sha256: hashes})
	if err != nil {
		return nil, fmt.Errorf("grpc request missing blobs: %w", err)
	}
	return resp.GetSha256(), nil
}

// UploadBlob читает данные чанками и стримит их с offset и final на последнем.
func (cl *Client) UploadBlob(ctx context.Context, sha256hex string, r io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, 4*cl.timeout())
	defer cancel()
	stream, err := cl.c.UploadBlob(ctx)
	if err != nil {
		return fmt.Errorf("grpc upload blob: open: %w", err)
	}
	buf := make([]byte, 64<<10)
	var offset uint64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			chunk := &syncv1.BlobChunk{Sha256: sha256hex, Offset: offset, Data: buf[:n]}
			offset += uint64(n)
			if rerr != nil {
				chunk.Final = true
			}
			if err := stream.Send(chunk); err != nil {
				return fmt.Errorf("grpc upload blob: send: %w", err)
			}
		}
		if rerr != nil {
			break
		}
	}
	ack, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("grpc upload blob: ack: %w", err)
	}
	if !ack.GetOk() {
		return fmt.Errorf("grpc upload blob: peer rejected %s: %s", sha256hex, ack.GetError())
	}
	return nil
}

// DownloadBlob собирает чанки в reader, проверяя непрерывность offset и хэш на приёме.
func (cl *Client) DownloadBlob(ctx context.Context, sha256hex string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*cl.timeout())
	defer cancel()
	stream, err := cl.c.DownloadBlob(ctx, &syncv1.BlobHash{Sha256: sha256hex})
	if err != nil {
		return nil, fmt.Errorf("grpc download blob: open: %w", err)
	}
	var (
		out    []byte
		expect uint64
	)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("grpc download blob: recv: %w", err)
		}
		if chunk.GetOffset() != expect {
			return nil, fmt.Errorf("grpc download blob: gap: want offset %d got %d", expect, chunk.GetOffset())
		}
		out = append(out, chunk.GetData()...)
		expect += uint64(len(chunk.GetData()))
		if chunk.GetFinal() {
			break
		}
	}
	return io.NopCloser(newSliceReader(out)), nil
}

// sliceReader — минимальный io.Reader по буферу.
type sliceReader struct {
	data []byte
	pos  int
}

func newSliceReader(b []byte) *sliceReader { return &sliceReader{data: b} }

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.pos:])
	s.pos += n
	return n, nil
}
