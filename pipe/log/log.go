package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/picosh/utils/pipe"
)

// ReconnectLogger is a logger that forwards log records to a remote log drain and reconnects even if the initial connection fails.
type ReconnectLogger struct {
	Logger        *slog.Logger
	SSHClientInfo *pipe.SSHClientInfo
	Buffer        int
	Timeout       time.Duration

	setupMu sync.Mutex

	Client  *pipe.Client
	Session *pipe.Session
}

func (r *ReconnectLogger) Setup() error {
	r.setupMu.Lock()
	defer r.setupMu.Unlock()

	if r.Client == nil {
		logWriter, err := pipe.NewClient(context.Background(), r.Logger, r.SSHClientInfo)
		if err != nil {
			return err
		}

		r.Client = logWriter
	}

	if r.Session == nil {
		s, err := r.Client.AddSession("rootLogger", "pub log-drain -b=false", r.Buffer, r.Timeout, r.Timeout)
		if err != nil {
			return err
		}

		r.Session = s
	}

	return nil
}

func (r *ReconnectLogger) Write(p []byte) (n int, err error) {
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

var _ io.Writer = (*ReconnectLogger)(nil)

// RegisterReconnectLogger registers a logger that forwards log records to a remote log drain and reconnects even if the initial connection fails.
func RegisterReconnectLogger(logger *slog.Logger, info *pipe.SSHClientInfo, buffer int, timeout time.Duration) (*slog.Logger, error) {
	reconnectLogger := &ReconnectLogger{
		Logger:        logger,
		SSHClientInfo: info,
		Buffer:        buffer,
		Timeout:       timeout,
	}

	go func() {
		for {
			err := reconnectLogger.Setup()
			if err != nil {
				logger.Error("could not setup reconnect logger", "error", err)
				time.Sleep(5 * time.Second)
				continue
			}

			break
		}
	}()

	currentHandler := logger.Handler()
	return slog.New(
		&MultiHandler{
			Handlers: []slog.Handler{
				currentHandler,
				slog.NewJSONHandler(reconnectLogger, &slog.HandlerOptions{
					AddSource: true,
					Level:     slog.LevelDebug,
				}),
			},
		},
	), nil
}

// RegisterLogger registers a logger that forwards log records to a remote log drain.
func RegisterLogger(logger *slog.Logger, info *pipe.SSHClientInfo, buffer int, timeout time.Duration) (*slog.Logger, error) {
	logWriter, err := pipe.NewClient(context.Background(), logger, info)
	if err != nil {
		return nil, err
	}

	s, err := logWriter.AddSession("rootLogger", "pub log-drain -b=false", buffer, timeout, timeout)
	if err != nil {
		return nil, err
	}

	currentHandler := logger.Handler()
	return slog.New(
		&MultiHandler{
			Handlers: []slog.Handler{
				currentHandler,
				slog.NewJSONHandler(s, &slog.HandlerOptions{
					AddSource: true,
					Level:     slog.LevelDebug,
				}),
			},
		},
	), nil
}

// ReadLogs reads logs from a remote log drain.
func ReadLogs(ctx context.Context, logger *slog.Logger, connectionInfo *pipe.SSHClientInfo) (io.Reader, error) {
	return pipe.Sub(ctx, logger, connectionInfo, "sub log-drain -k")
}
