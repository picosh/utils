package log

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
)

// MultiHandler is a handler that forwards log records to multiple handlers.
type MultiHandler struct {
	Handlers []slog.Handler
	mu       sync.Mutex
}

// Enabled returns true if at least one of the handlers is enabled for the given level.
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

// Handle forwards the log record to all handlers.
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

// WithAttrs returns a new MultiHandler with the given attributes added to each handler.
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

// WithGroup returns a new MultiHandler with the given group name added to each handler.
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
