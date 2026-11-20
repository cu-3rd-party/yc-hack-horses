package http

import (
	"log/slog"
	"sync"
)

// Hub — шина новых событий для WS-подписчиков. Публикация неблокирующая:
// подписчик с переполненным буфером пропускает кадры (для UI это допустимо,
// недостающие события добираются через REST cursor).
type Hub struct {
	mu     sync.Mutex
	nextID int
	subs   map[int]chan []byte
}

// NewHub создаёт пустую шину.
func NewHub() *Hub { return &Hub{subs: map[int]chan []byte{}} }

// Subscribe регистрирует подписчика и возвращает канал кадров и функцию отписки.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subs[id] = ch
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

// Publish рассылает кадр всем подписчикам, не блокируясь на медленных.
func (h *Hub) Publish(frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.subs {
		select {
		case ch <- frame:
		default:
			slog.Warn("ws subscriber slow, frame dropped", "subscriber", id, "bytes", len(frame))
		}
	}
}
