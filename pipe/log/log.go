package log

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/picosh/utils/pipe"
)

const (
	ID         = "logger"
	Topic      = "log-drain"
	PubCommand = "pub " + Topic + " -b=false"
	SubCommand = "sub " + Topic + " -k"
)

// RegisterReconnectLogger registers a logger that forwards log records to a remote log drain and reconnects even if the initial connection fails.
func RegisterReconnectLogger(ctx context.Context, logger *slog.Logger, info *pipe.SSHClientInfo, buffer int, timeout time.Duration) *slog.Logger {
	reconnectLogger := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		ID,
		PubCommand,
		buffer,
		timeout,
	)

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
	)
}

// ReconnectReadLogs reads metrics from a remote metric drain and reconnects even if the initial connection fails.
func ReconnectReadLogs(ctx context.Context, logger *slog.Logger, connectionInfo *pipe.SSHClientInfo, buffer int, timeout time.Duration) io.Reader {
	reconnectMetricReader := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		connectionInfo,
		ID,
		PubCommand,
		buffer,
		timeout,
	)

	return reconnectMetricReader
}

// ReadLogs reads logs from a remote log drain.
func ReadLogs(ctx context.Context, logger *slog.Logger, connectionInfo *pipe.SSHClientInfo) (io.Reader, error) {
	return pipe.Sub(ctx, logger, connectionInfo, SubCommand)
}
