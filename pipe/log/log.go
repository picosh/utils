package log

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/picosh/utils/pipe"
)

func RegisterLogger(logger *slog.Logger, info *pipe.SSHClientInfo, buffer int, timeout time.Duration) (*slog.Logger, error) {
	if buffer < 0 {
		buffer = 0
	}

	logWriter, err := pipe.NewClient(logger, info)
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

func ReadLogs(ctx context.Context, connectionInfo *pipe.SSHClientInfo) (io.Reader, error) {
	return pipe.Sub("sub log-drain -k", ctx, connectionInfo)
}
