// Package grpc — gRPC-транспорт синхронизации: сервер SyncService и клиент-адаптер
// Peer, реализующий интерфейс internal/sync поверх сгенерированного кода.
package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	syncv1 "github.com/cu3rd/yc-hack-horses/backend/api/gen/sync/v1"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// ServerDeps — зависимости сервера SyncService.
type ServerDeps struct {
	Events   storage.EventStore
	Blobs    storage.BlobStore // может быть nil
	Identity syncengine.NodeIdentity
	Log      *slog.Logger
}

// Server — реализация syncv1.SyncServiceServer поверх хранилища ноды.
// Вся логика идемпотентности — в EventStore.Append; сервер только маппит протокол.
type Server struct {
	syncv1.UnimplementedSyncServiceServer
	deps ServerDeps
	log  *slog.Logger
}

// NewServer собирает сервер.
func NewServer(deps ServerDeps) *Server {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &Server{deps: deps, log: log}
}

// Handshake — рукопожатие: обмен версиями и ролями.
func (s *Server) Handshake(ctx context.Context, req *syncv1.HandshakeRequest) (*syncv1.HandshakeResponse, error) {
	s.log.Debug("handshake", "peer", req.GetNodeId(), "protocol", req.GetProtocolVersion())
	return &syncv1.HandshakeResponse{
		NodeId:          s.deps.Identity.NodeID,
		ProtocolVersion: syncengine.ProtocolVersion,
		AppVersion:      s.deps.Identity.AppVersion,
		Role:            s.deps.Identity.Role,
	}, nil
}

// GetNodeInfo — сводка о ноде для мониторинга.
func (s *Server) GetNodeInfo(ctx context.Context, _ *syncv1.GetNodeInfoRequest) (*syncv1.NodeInfo, error) {
	clock, err := s.deps.Events.GetVectorClock(ctx)
	if err != nil {
		return nil, fmt.Errorf("node info: vector clock: %w", err)
	}
	var total uint64
	for _, seq := range clock {
		total += seq
	}
	lastSeq := int64(0)
	if len(clock) > 0 {
		for _, seq := range clock {
			lastSeq = max(lastSeq, int64(seq))
		}
	}
	return &syncv1.NodeInfo{
		NodeId:     s.deps.Identity.NodeID,
		AppVersion: s.deps.Identity.AppVersion,
		EventCount: total,
		LastSeq:    lastSeq,
		Role:       s.deps.Identity.Role,
	}, nil
}

// ExchangeVectorClock возвращает часы этой ноды.
func (s *Server) ExchangeVectorClock(ctx context.Context, _ *syncv1.VectorClock) (*syncv1.VectorClock, error) {
	clock, err := s.deps.Events.GetVectorClock(ctx)
	if err != nil {
		return nil, fmt.Errorf("exchange clock: %w", err)
	}
	return clockToProto(clock), nil
}

// PullEvents отдаёт события, отсутствующие у клиента (по его clock), батчами
// до req.GetLimit(); последний батч помечен final.
func (s *Server) PullEvents(req *syncv1.PullEventsRequest, stream syncv1.SyncService_PullEventsServer) error {
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 256
	}
	missing, err := s.deps.Events.GetMissingEvents(stream.Context(), clockFromProto(req.GetHave()))
	if err != nil {
		return fmt.Errorf("pull events: select missing: %w", err)
	}
	for start := 0; start < len(missing); start += limit {
		end := min(start+limit, len(missing))
		batch, err := eventsToProto(missing[start:end])
		if err != nil {
			return fmt.Errorf("pull events: encode batch: %w", err)
		}
		if err := stream.Send(&syncv1.EventBatch{Events: batch, Final: end >= len(missing)}); err != nil {
			return fmt.Errorf("pull events: send batch: %w", err)
		}
	}
	return nil
}

// PushEvents принимает клиентский стрим батчей, пишет через Append (идемпотентно)
// и подтверждает записанные event_id.
func (s *Server) PushEvents(stream syncv1.SyncService_PushEventsServer) error {
	var (
		acked     []string
		ackedThru uint64
	)
	for {
		batch, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("push events: recv: %w", err)
		}
		events, err := eventsFromProto(batch.GetEvents())
		if err != nil {
			return fmt.Errorf("push events: decode batch: %w", err)
		}
		if err := s.deps.Events.Append(stream.Context(), events); err != nil {
			return fmt.Errorf("push events: append: %w", err)
		}
		for _, e := range events {
			acked = append(acked, e.EventID)
			ackedThru = max(ackedThru, e.Sequence)
		}
	}
	return stream.SendAndClose(&syncv1.AckBatch{
		AckEventIds:  acked,
		AckedThrough: ackedThru,
		NodeId:       s.deps.Identity.NodeID,
	})
}

// RequestMissingBlobs — какие из запрошенных хэшей у этой ноды отсутствуют.
func (s *Server) RequestMissingBlobs(ctx context.Context, req *syncv1.BlobHashList) (*syncv1.BlobHashList, error) {
	if s.deps.Blobs == nil {
		return nil, errors.New("blob storage not configured")
	}
	missing, err := s.deps.Blobs.ListMissing(ctx, req.GetSha256())
	if err != nil {
		return nil, fmt.Errorf("request missing blobs: %w", err)
	}
	return &syncv1.BlobHashList{Sha256: missing, NodeId: s.deps.Identity.NodeID}, nil
}

// UploadBlob принимает клиентский стрим чанков, собирает blob, проверяет sha256
// и пишет (дедупликация: уже существующий — no-op).
func (s *Server) UploadBlob(stream syncv1.SyncService_UploadBlobServer) error {
	var (
		sha    string
		expect uint64
		buf    []byte
	)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("upload blob: recv: %w", err)
		}
		if chunk.GetSha256() == "" {
			return errors.New("upload blob: empty sha256")
		}
		if sha == "" {
			sha = chunk.GetSha256()
		} else if chunk.GetSha256() != sha {
			return fmt.Errorf("upload blob: sha256 changed mid-stream")
		}
		if chunk.GetOffset() != expect {
			return fmt.Errorf("upload blob: gap in stream: want offset %d got %d", expect, chunk.GetOffset())
		}
		buf = append(buf, chunk.GetData()...)
		expect += uint64(len(chunk.GetData()))
	}
	if sha == "" {
		return errors.New("upload blob: no chunks received")
	}
	if err := s.storeBlob(stream.Context(), sha, buf); err != nil {
		return err
	}
	return stream.SendAndClose(&syncv1.BlobAck{Sha256: sha, Ok: true})
}

func (s *Server) storeBlob(ctx context.Context, sha256hex string, data []byte) error {
	has, err := s.deps.Blobs.Has(ctx, sha256hex)
	if err != nil {
		return fmt.Errorf("upload blob: check %s: %w", sha256hex, err)
	}
	if has {
		return nil // дедупликация
	}
	if err := s.deps.Blobs.Put(ctx, sha256hex, "", data); err != nil {
		return fmt.Errorf("upload blob: store %s: %w", sha256hex, err)
	}
	return nil
}

// DownloadBlob отдаёт blob чанками с offset; последний чанк помечен final.
func (s *Server) DownloadBlob(req *syncv1.BlobHash, stream syncv1.SyncService_DownloadBlobServer) error {
	data, err := s.deps.Blobs.Get(stream.Context(), req.GetSha256())
	if err != nil {
		return fmt.Errorf("download blob %s: %w", req.GetSha256(), err)
	}
	const chunkSize = 64 << 10
	if len(data) == 0 {
		return fmt.Errorf("download blob %s: empty", req.GetSha256())
	}
	for offset := 0; offset < len(data); offset += chunkSize {
		end := min(offset+chunkSize, len(data))
		if err := stream.Send(&syncv1.BlobChunk{
			Sha256: req.GetSha256(),
			Offset: uint64(offset),
			Data:   data[offset:end],
			Final:  end >= len(data),
		}); err != nil {
			return fmt.Errorf("download blob: send chunk: %w", err)
		}
	}
	return nil
}

// --- маппинг proto <-> domain ---

func clockFromProto(c *syncv1.VectorClock) storage.VectorClock {
	out := make(storage.VectorClock, len(c.GetClocks()))
	for node, seq := range c.GetClocks() {
		out[node] = seq
	}
	return out
}

func clockToProto(c storage.VectorClock) *syncv1.VectorClock {
	out := make(map[string]uint64, len(c))
	for node, seq := range c {
		out[node] = seq
	}
	return &syncv1.VectorClock{Clocks: out}
}

func eventToProto(e domain.Event) *syncv1.Event {
	return &syncv1.Event{
		EventId:         e.EventID,
		NodeId:          e.NodeID,
		Sequence:        e.Sequence,
		EventType:       e.EventType,
		SchemaVersion:   int32(e.SchemaVersion),
		AggregateId:     e.AggregateID,
		OccurredAtUnixMs: e.OccurredAt.UnixMilli(),
		Payload:         []byte(e.Payload),
		PayloadHash:     e.PayloadHash(),
		ModelVersion:    e.ModelVersion,
	}
}

func eventFromProto(e *syncv1.Event) (domain.Event, error) {
	ev := domain.Event{
		EventID:      e.GetEventId(),
		NodeID:       e.GetNodeId(),
		Sequence:     e.GetSequence(),
		EventType:    e.GetEventType(),
		SchemaVersion: int(e.GetSchemaVersion()),
		AggregateID:  e.GetAggregateId(),
		OccurredAt:   time.UnixMilli(e.GetOccurredAtUnixMs()).UTC(),
		Payload:      json.RawMessage(e.GetPayload()),
		ModelVersion: e.GetModelVersion(),
	}
	if e.GetPayloadHash() != "" && e.GetPayloadHash() != ev.PayloadHash() {
		return domain.Event{}, fmt.Errorf("event %s: payload hash mismatch", ev.EventID)
	}
	if err := ev.Validate(); err != nil {
		return domain.Event{}, err
	}
	return ev, nil
}

func eventsToProto(events []domain.Event) ([]*syncv1.Event, error) {
	out := make([]*syncv1.Event, 0, len(events))
	for _, e := range events {
		out = append(out, eventToProto(e))
	}
	return out, nil
}

func eventsFromProto(events []*syncv1.Event) ([]domain.Event, error) {
	out := make([]domain.Event, 0, len(events))
	for _, e := range events {
		ev, err := eventFromProto(e)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}
