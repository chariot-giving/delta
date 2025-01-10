package delta

import (
	"context"
	"errors"

	"github.com/chariot-giving/delta/deltacommon"
)

var errClientNotInContext = errors.New("river: client not found in context, can only be used in a Worker")

func withClient[TTx any](ctx context.Context, client *Client[TTx]) context.Context {
	return context.WithValue(ctx, deltacommon.ContextKeyClient{}, client)
}
