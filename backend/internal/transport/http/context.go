package http

import (
	"context"
	"time"
)

// contextWithTimeout — короткий дедлайн для side-операций хендлеров.
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// contextWithoutCancel отвязывает хендлер от запроса (WS живёт дольше запроса).
func contextWithoutCancel(parent context.Context) context.Context {
	return context.WithoutCancel(parent)
}
