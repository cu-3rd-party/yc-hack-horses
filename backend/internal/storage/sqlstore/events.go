package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// Append пишет события идемпотентно: повтор (node_id, sequence) — no-op
// (ON CONFLICT DO NOTHING), первый записанный вариант побеждает.
func (s *Store) Append(ctx context.Context, events []domain.Event) error {
	const q = `INSERT INTO events
		(event_id, node_id, sequence, event_type, schema_version, aggregate_id, occurred_at, payload, payload_hash, model_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id, sequence) DO NOTHING`
	for i := range events {
		ev := events[i]
		if err := ev.Validate(); err != nil {
			return fmt.Errorf("sqlstore: append %d: %w", i, err)
		}
		payload, err := json.Marshal(json.RawMessage(ev.Payload))
		if err != nil {
			return fmt.Errorf("sqlstore: append %d: payload: %w", i, err)
		}
		if _, err := s.exec(ctx, q,
			ev.EventID, ev.NodeID, int64(ev.Sequence), ev.EventType, ev.SchemaVersion,
			ev.AggregateID, ev.OccurredAt.UnixMilli(), payload, ev.PayloadHash(), ev.ModelVersion,
		); err != nil {
			return fmt.Errorf("sqlstore: append %s/%s#%d: %w", ev.EventType, ev.NodeID, ev.Sequence, err)
		}
	}
	return nil
}

// GetEvents возвращает журнал строго после cursor, упорядоченно по (node_id, sequence).
// Пустой NodeID курсора — начало журнала (все ноды).
func (s *Store) GetEvents(ctx context.Context, cursor storage.Cursor, limit int) ([]domain.Event, error) {
	var (
		conds []string
		args  []any
	)
	if cursor.NodeID != "" {
		conds = append(conds, "(node_id = ? AND sequence > ?) OR (node_id > ?)")
		args = append(args, cursor.NodeID, int64(cursor.Sequence), cursor.NodeID)
	}
	q := `SELECT event_id, node_id, sequence, event_type, schema_version, aggregate_id, occurred_at, payload, model_version FROM events`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " OR ")
	}
	q += ` ORDER BY node_id, sequence`
	if limit <= 0 {
		limit = 1000
	}
	q += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: get events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// GetVectorClock возвращает node_id -> последний sequence.
func (s *Store) GetVectorClock(ctx context.Context) (storage.VectorClock, error) {
	rows, err := s.query(ctx, `SELECT node_id, MAX(sequence) FROM events GROUP BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: vector clock: %w", err)
	}
	defer rows.Close()
	clock := storage.VectorClock{}
	for rows.Next() {
		var nodeID string
		var seq int64
		if err := rows.Scan(&nodeID, &seq); err != nil {
			return nil, fmt.Errorf("sqlstore: vector clock scan: %w", err)
		}
		clock[nodeID] = uint64(seq)
	}
	return clock, rows.Err()
}

// GetMissingEvents возвращает события, которых нет у удалённой стороны:
// всё, что после remote[node] по каждой известной remote ноде, плюс все события
// нод, которых в remote clock вообще нет.
func (s *Store) GetMissingEvents(ctx context.Context, remote storage.VectorClock) ([]domain.Event, error) {
	var (
		conds []string
		args  []any
	)
	known := make([]string, 0, len(remote))
	for nodeID, seq := range remote {
		known = append(known, nodeID)
		conds = append(conds, "(node_id = ? AND sequence > ?)")
		args = append(args, nodeID, int64(seq))
	}
	if len(known) > 0 {
		placeholders := make([]string, len(known))
		for i, id := range known {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conds = append(conds, "node_id NOT IN ("+strings.Join(placeholders, ", ")+")")
	}
	q := `SELECT event_id, node_id, sequence, event_type, schema_version, aggregate_id, occurred_at, payload, model_version FROM events`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " OR ")
	}
	q += ` ORDER BY node_id, sequence LIMIT 10000`

	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: missing events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// LastSequence возвращает последний sequence ноды (0, если событий нет).
func (s *Store) LastSequence(ctx context.Context, nodeID string) (uint64, error) {
	var seq sql.NullInt64
	err := s.queryRow(ctx, `SELECT MAX(sequence) FROM events WHERE node_id = ?`, nodeID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("sqlstore: last sequence %s: %w", nodeID, err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return uint64(seq.Int64), nil
}

func scanEvents(rows *sql.Rows) ([]domain.Event, error) {
	var out []domain.Event
	for rows.Next() {
		var (
			ev      domain.Event
			occMs   int64
			seq     int64
			payload []byte
		)
		if err := rows.Scan(&ev.EventID, &ev.NodeID, &seq, &ev.EventType, &ev.SchemaVersion,
			&ev.AggregateID, &occMs, &payload, &ev.ModelVersion); err != nil {
			return nil, fmt.Errorf("sqlstore: scan event: %w", err)
		}
		ev.Sequence = uint64(seq)
		ev.OccurredAt = timeFromUnixMilli(occMs)
		ev.Payload = json.RawMessage(payload)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: iterate events: %w", err)
	}
	return out, nil
}

// notFound переводит sql.ErrNoRows в storage.ErrNotFound с сохранением контекста.
func notFound(what string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, storage.ErrNotFound)
	}
	return err
}
