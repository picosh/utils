package log

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
)

type MultiHandler struct {
	Handlers []slog.Handler
	mu       sync.Mutex
}

func (m *MultiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, h := range m.Handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}

	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, h := range m.Handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}

	return errors.Join(errs...)
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()

	var handlers []slog.Handler

	for _, h := range m.Handlers {
		handlers = append(handlers, h.WithAttrs(slices.Clone(attrs)))
	}

	return &MultiHandler{
		Handlers: handlers,
	}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return m
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var handlers []slog.Handler

	for _, h := range m.Handlers {
		handlers = append(handlers, h.WithGroup(name))
	}

	return &MultiHandler{
		Handlers: handlers,
	}
}

var _ slog.Handler = (*MultiHandler)(nil)
