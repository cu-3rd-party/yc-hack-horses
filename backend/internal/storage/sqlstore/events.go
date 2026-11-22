package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
		// Кэш сводки недействителен: журнал изменился (даже no-op повтор
		// при ON CONFLICT ничего не вставляет, но инвалидация на повторе
		// безопасна — следующее чтение просто пересчитает GROUP BY).
		s.invalidateSummary()
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

// summaryCache — кэш сводки (eventCount + векторные часы) для /node и
// WS-handshake: без кэша каждый запрос делает COUNT(*) и GROUP BY по всем нодам.
// Инвалидация — при успешной вставке своего Append; TTL страхует от записей
// других экземпляров Store поверх той же базы (тесты, админ-скрипты).
type summaryCache struct {
	mu sync.Mutex
	// invalidSeq — число инвалидаций; снапшот считается валидным, только пока
	// его seq равен текущему. Append инвалидирует внутри tx (до коммита), а
	// RunInTx — ещё раз после COMMIT: снапшот, снятый между инвалидацией и
	// коммитом, закэшировал бы пред-коммитное значение, и повторная
	// инвалидация его отбрасывает. TTL страхует остальные окна.
	invalidSeq uint64
	// Части сводки независимы: заполнение часов не должно «омолаживать» count
	// (и наоборот), поэтому у каждой части своё время снятия. validSeq —
	// счётчик инвалидаций на момент последнего заполнения любой части.
	countFilled   bool
	count         int64
	countAt       time.Time
	countValidSeq uint64
	clock         storage.VectorClock
	clockAt       time.Time
	clockValidSeq uint64
}

// summaryCacheTTL — допустимый возраст кэша. Хватает, чтобы сгладить всплеск
// запросов к /node, но не переживает секундные паузы активной записи.
const summaryCacheTTL = time.Second

func (s *Store) invalidateSummary() {
	if s.summary == nil {
		return
	}
	s.summary.mu.Lock()
	s.summary.invalidSeq++
	s.summary.mu.Unlock()
}

// freshCount — годен ли кэш счётчика событий.
func (c *summaryCache) freshCount() (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := c.countFilled && c.countValidSeq == c.invalidSeq && time.Since(c.countAt) < summaryCacheTTL
	return c.count, ok
}

// freshClock — годен ли кэш векторных часов. Возвращает копию map: вызыватель
// (sync engine) мог бы мутировать внутренний кэш через ссылку.
func (c *summaryCache) freshClock() (storage.VectorClock, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := c.clock != nil && c.clockValidSeq == c.invalidSeq && time.Since(c.clockAt) < summaryCacheTTL
	if !ok {
		return nil, false
	}
	out := make(storage.VectorClock, len(c.clock))
	for k, v := range c.clock {
		out[k] = v
	}
	return out, true
}

// fillCount / fillClock записывают пересчитанные значения, если за время
// SQL-запроса (before — снимок счётчика инвалидаций до запроса) не было
// инвалидации: иначе запрос увидел базу до коммита параллельного Append и
// результат обязан быть отброшен. Время снятия ставится только своей части —
// заполнение часов не «омолаживает» count и наоборот.
func (c *summaryCache) fillCount(before uint64, n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invalidSeq != before {
		return
	}
	c.count, c.countFilled, c.countAt = n, true, time.Now()
	c.countValidSeq = before
}

func (c *summaryCache) fillClock(before uint64, clock storage.VectorClock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invalidSeq != before {
		return
	}
	c.clock, c.clockAt = clock, time.Now()
	c.clockValidSeq = before
}

// invalidationSeq — снимок счётчика инвалидаций ДО запуска SQL-запроса.
func (c *summaryCache) invalidationSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalidSeq
}

// GetVectorClock возвращает node_id -> последний sequence (с краткосрочным кэшем).
func (s *Store) GetVectorClock(ctx context.Context) (storage.VectorClock, error) {
	var before uint64
	if s.summary != nil {
		if clock, ok := s.summary.freshClock(); ok {
			return clock, nil
		}
		before = s.summary.invalidationSeq()
	}
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: vector clock: %w", err)
	}
	if s.summary != nil {
		s.summary.fillClock(before, clock)
	}
	return clock, nil
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

// MissingEventsCursor — позиция продолжения в потоке недостающих событий:
// последнее отданное событие в порядке ORDER BY node_id, sequence.
type MissingEventsCursor struct {
	NodeID   string
	Sequence uint64
}

// GetMissingEventsPaged — GetMissingEvents с продолжением: отдаёт не больше
// limit событий после курсора и следующий курсор (nil — поток исчерпан).
// Позволяет синку/grpc докачивать журнал батчами без буферизации всего
// недостающего хвоста в памяти. limit <= 0 -> 1000, > 10000 -> 10000.
func (s *Store) GetMissingEventsPaged(ctx context.Context, remote storage.VectorClock, cursor MissingEventsCursor, limit int) ([]domain.Event, *MissingEventsCursor, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	missingConds, missingArgs := missingEventConds(remote)
	var conds []string
	var args []any
	if missingConds != "" {
		conds = append(conds, "("+missingConds+")")
		args = append(args, missingArgs...)
	}
	if cursor.NodeID != "" {
		conds = append(conds, "(node_id > ? OR (node_id = ? AND sequence > ?))")
		args = append(args, cursor.NodeID, cursor.NodeID, int64(cursor.Sequence))
	}
	q := `SELECT event_id, node_id, sequence, event_type, schema_version, aggregate_id, occurred_at, payload, model_version FROM events`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " AND ")
	}
	q += ` ORDER BY node_id, sequence LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlstore: missing events: %w", err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return nil, nil, err
	}
	if len(events) > limit {
		last := events[limit-1]
		return events[:limit], &MissingEventsCursor{NodeID: last.NodeID, Sequence: last.Sequence}, nil
	}
	return events, nil, nil
}

// missingEventConds собирает условия «события, отсутствующие у remote»:
// всё после remote[node] по каждой известной ноде плюс все ноды, которых
// в remote clock нет. Общая для GetMissingEvents и GetMissingEventsPaged.
func missingEventConds(remote storage.VectorClock) (string, []any) {
	var conds []string
	var args []any
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
	if len(conds) == 0 {
		return "", nil
	}
	return strings.Join(conds, " OR "), args
}

// LastSequence возвращает последний sequence ноды (0, если событий нет).
func (s *Store) LastSequence(ctx context.Context, nodeID string) (uint64, error) {
	// Local writers allocate the next sequence inside this transaction. Two
	// PostgreSQL sessions must not read the same MAX before either commits.
	if s.tx != nil && s.driver == "postgres" {
		if _, err := s.exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", nodeID); err != nil {
			return 0, err
		}
	}
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
