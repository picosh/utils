package log

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/picosh/utils/pipe"
)

// RegisterLogger registers a logger that forwards log records to a remote log drain.
func RegisterLogger(logger *slog.Logger, info *pipe.SSHClientInfo, buffer int, timeout time.Duration) (*slog.Logger, error) {
	if buffer < 0 {
		buffer = 0
	}

	logWriter, err := pipe.NewClient(context.Background(), logger, info)
	if err != nil {
		return nil, err
	}

	s, err := logWriter.AddSession("rootLogger", "pub log-drain -b=false", buffer, timeout)
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
