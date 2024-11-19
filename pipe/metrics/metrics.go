package metrics

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/picosh/utils/pipe"
)

const (
	ID         = "metrics"
	Topic      = "metric-drain"
	PubCommand = "pub " + Topic + " -b=false"
	SubCommand = "sub " + Topic + " -k"
)

// ReconnectReadMetrics reads metrics from a remote metric drain and reconnects even if the initial connection fails.
func ReconnectReadMetrics(ctx context.Context, logger *slog.Logger, connectionInfo *pipe.SSHClientInfo, buffer int, timeout time.Duration) io.Reader {
	reconnectMetricReader := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		connectionInfo,
		ID,
		SubCommand,
		buffer,
		timeout,
	)

	return reconnectMetricReader
}

// ReadLogs reads metrics from a metric drain.
func ReadMetrics(ctx context.Context, logger *slog.Logger, connectionInfo *pipe.SSHClientInfo) (io.Reader, error) {
	return pipe.Sub(ctx, logger, connectionInfo, SubCommand)
}
