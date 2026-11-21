package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"

	"github.com/coder/websocket"
)

// streamEvents — WS-поток новых событий. Первый кадр: векторные часы,
// далее — кадры {"type":"event","event":…} по мере Append-ов.
func streamEvents(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			slog.Warn("ws accept failed", "err", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx := contextWithoutCancel(r.Context())

		// Queue events during the initial snapshot so a client can write as
		// soon as it receives the clock without missing the resulting event.
		frames, unsubscribe := d.Hub.Subscribe()
		defer unsubscribe()

		clock, err := d.Store.GetVectorClock(ctx)
		if err != nil {
			slog.Error("ws: vector clock", "err", err)
			conn.Close(websocket.StatusInternalError, "vector clock failed")
			return
		}
		clockFrame, _ := json.Marshal(map[string]any{"type": "clock", "clock": clock})
		if err := conn.Write(ctx, websocket.MessageText, clockFrame); err != nil {
			return
		}

		// WS живёт до разрыва клиента; пинг держит соединение и проверяет живость.
		pingTicker := time.NewTicker(20 * time.Second)
		defer pingTicker.Stop()
		readCtx, stopRead := context.WithCancel(ctx)
		defer stopRead()
		go func() {
			// читаем pings/close от клиента
			for {
				if _, _, err := conn.Read(readCtx); err != nil {
					stopRead()
					return
				}
			}
		}()

		for {
			select {
			case <-readCtx.Done():
				return
			case frame := <-frames:
				writeCtx, writeCancel := contextWithTimeout(ctx, 5*time.Second)
				if err := conn.Write(writeCtx, websocket.MessageText, frame); err != nil {
					writeCancel()
					return
				}
				writeCancel()
			case <-pingTicker.C:
				pingCtx, pingCancel := contextWithTimeout(ctx, 5*time.Second)
				if err := conn.Ping(pingCtx); err != nil {
					pingCancel()
					return
				}
				pingCancel()
			}
		}
	}
}

// publishEvent формирует кадр события и рассылает подписчикам.
func publishEvent(d Deps, ev domain.Event) {
	frame, err := json.Marshal(map[string]any{"type": "event", "event": ev})
	if err != nil {
		slog.Error("ws: marshal event frame", "err", err)
		return
	}
	d.Hub.Publish(frame)
}
