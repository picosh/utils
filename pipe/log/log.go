package log

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/picosh/utils/pipe"
)

// ReconnectLogger is a logger that forwards log records to a remote log drain and reconnects even if the initial connection fails.
type ReconnectLogger struct {
	Logger        *slog.Logger
	SSHClientInfo *pipe.SSHClientInfo
	Buffer        int
	Timeout       time.Duration

	Client  *pipe.Client
	Session *pipe.Session
}

func (r *ReconnectLogger) Write(p []byte) (n int, err error) {
	if r.Client == nil {
		logWriter, err := pipe.NewClient(context.Background(), r.Logger, r.SSHClientInfo)
		if err != nil {
			return 0, err
		}

		r.Client = logWriter
	}

	if r.Session == nil {
		s, err := r.Client.AddSession("rootLogger", "pub log-drain -b=false", r.Buffer, r.Timeout, r.Timeout)
		if err != nil {
			return 0, err
		}

		r.Session = s
	}

	return r.Session.Write(p)
}

var _ io.Writer = (*ReconnectLogger)(nil)

// RegisterReconnectLogger registers a logger that forwards log records to a remote log drain and reconnects even if the initial connection fails.
func RegisterReconnectLogger(logger *slog.Logger, info *pipe.SSHClientInfo, buffer int, timeout time.Duration) (*slog.Logger, error) {
	currentHandler := logger.Handler()
	return slog.New(
		&MultiHandler{
			Handlers: []slog.Handler{
				currentHandler,
				slog.NewJSONHandler(&ReconnectLogger{
					Logger:        logger,
					SSHClientInfo: info,
					Buffer:        buffer,
					Timeout:       timeout,
				}, &slog.HandlerOptions{
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
