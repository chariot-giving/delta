package middleware

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
)

func TestLoggingMiddleware(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		err       error
		wantLevel string
		wantMsg   string
	}{
		{name: "Success", err: nil, wantLevel: "INFO", wantMsg: "job completed"},
		{name: "Failure", err: errors.New("boom"), wantLevel: "ERROR", wantMsg: "job failed"},
		{name: "Cancel", err: river.JobCancel(errors.New("resource deleted")), wantLevel: "INFO", wantMsg: "job cancelled"},
		{name: "Snooze", err: river.JobSnooze(time.Minute), wantLevel: "INFO", wantMsg: "job snoozed"},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			middleware := NewLoggingMiddleware(slog.New(slog.NewTextHandler(&buf, nil)))

			err := middleware.Work(context.Background(), &rivertype.JobRow{ID: 1, Kind: "test"}, func(context.Context) error { return tt.err })
			require.ErrorIs(t, err, tt.err)

			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			last := string(lines[len(lines)-1])
			require.Contains(t, last, "level="+tt.wantLevel)
			require.Contains(t, last, `msg="`+tt.wantMsg+`"`)
		})
	}
}
