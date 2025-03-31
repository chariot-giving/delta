package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river/rivertype"
)

type contextKeyLogger struct{}

type loggingMiddleware struct {
	logger *slog.Logger
}

func NewLoggingMiddleware(logger *slog.Logger) rivertype.WorkerMiddleware {
	return &loggingMiddleware{logger: logger}
}

func (m *loggingMiddleware) IsMiddleware() bool {
	return true
}

func (m *loggingMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	logger := m.logger.WithGroup("riverjob").With("job_id", job.ID).With("job_kind", job.Kind)
	newCtx := context.WithValue(ctx, contextKeyLogger{}, logger)

	now := time.Now()
	logger.InfoContext(ctx, "starting job", "started_at", now.Format(time.RFC3339))
	err := doInner(newCtx)
	if err != nil {
		logger.ErrorContext(ctx, "job failed", "error", err, "finished_at", time.Now().Format(time.RFC3339), "duration", time.Since(now).Milliseconds())
	} else {
		logger.InfoContext(ctx, "job completed", "finished_at", time.Now().Format(time.RFC3339), "duration", time.Since(now).Milliseconds())
	}
	return err
}

// LoggerFromContext returns the logger from the context. If the logger is not found, it returns the default logger.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	logger, ok := ctx.Value(contextKeyLogger{}).(*slog.Logger)
	if !ok {
		return slog.Default()
	}
	return logger
}
