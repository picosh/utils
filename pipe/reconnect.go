package pipe

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// ReconnectReadWriteCloser reconnects even if the initial connection fails.
type ReconnectReadWriteCloser struct {
	Context       context.Context
	Logger        *slog.Logger
	SSHClientInfo *SSHClientInfo
	Buffer        int
	Timeout       time.Duration

	ID      string
	Command string

	setupMu sync.Mutex

	Client  *Client
	Session *Session
}

// Setup sets up the reconnect writer.
func (r *ReconnectReadWriteCloser) Setup() error {
	r.setupMu.Lock()
	defer r.setupMu.Unlock()

	if r.Client == nil {
		logWriter, err := NewClient(r.Context, r.Logger, r.SSHClientInfo)
		if err != nil {
			return err
		}

		r.Client = logWriter
	}

	if r.Session == nil {
		s, err := r.Client.AddSession(r.ID, r.Command, r.Buffer, r.Timeout, r.Timeout)
		if err != nil {
			return err
		}

		r.Session = s
	}

	return nil
}

// Write writes to the reconnect connection.
func (r *ReconnectReadWriteCloser) Write(p []byte) (n int, err error) {
	ok := r.setupMu.TryLock()
	if !ok {
		return 0, fmt.Errorf("could not acquire lock")
	}

	defer r.setupMu.Unlock()

	if r.Session == nil {
		return 0, fmt.Errorf("session is not viable, waiting for reconnect")
	}

	return r.Session.Write(p)
}

// Read reads from reconnect connection.
func (r *ReconnectReadWriteCloser) Read(p []byte) (n int, err error) {
	ok := r.setupMu.TryLock()
	if !ok {
		return 0, fmt.Errorf("could not acquire lock")
	}

	defer r.setupMu.Unlock()

	if r.Session == nil {
		return 0, fmt.Errorf("session is not viable, waiting for reconnect")
	}

	return r.Session.Read(p)
}

// Close closes the reconnect connection.
func (r *ReconnectReadWriteCloser) Close() error {
	ok := r.setupMu.TryLock()
	if !ok {
		return fmt.Errorf("could not acquire lock")
	}

	defer r.setupMu.Unlock()

	if r.Session == nil {
		return fmt.Errorf("session is not viable, waiting for reconnect")
	}

	return r.Session.Close()
}

var _ io.ReadWriteCloser = (*ReconnectReadWriteCloser)(nil)

// NewReconnectWriter creates a new writer and reconnects even if the initial connection fails.
func NewReconnectReadWriteCloser(context context.Context, logger *slog.Logger, info *SSHClientInfo, id, command string, buffer int, timeout time.Duration) *ReconnectReadWriteCloser {
	reconnectWriter := &ReconnectReadWriteCloser{
		Context:       context,
		Logger:        logger,
		SSHClientInfo: info,
		Buffer:        buffer,
		Timeout:       timeout,
		ID:            id,
		Command:       command,
	}

	go func() {
		for {
			err := reconnectWriter.Setup()
			if err != nil {
				logger.Error("could not setup reconnect writer", "error", err)

				select {
				case <-time.After(5 * time.Second):
					continue
				case <-context.Done():
					return
				}
			}

			break
		}
	}()

	return reconnectWriter
}
