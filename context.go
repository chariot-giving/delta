package delta

import (
	"context"
	"errors"

	"github.com/riverqueue/river/rivertype"
)

type contextKeyClient struct{}

var errClientNotInContext = errors.New("delta: client not found in context, can only be used in a Controller")

func withClient(ctx context.Context, client *Client) context.Context {
	return context.WithValue(ctx, contextKeyClient{}, client)
}

type jobContextMiddleware struct {
	client *Client
}

func (m *jobContextMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	return doInner(withClient(ctx, m.client))
}

// ClientFromContext returns the Client from the context. This function can
// only be used within a Controller's Work() or Inform() methods because that is the only place
// Delta sets the Client on the context.
//
// It panics if the context does not contain a Client, which will never happen
// from the context provided to a Controller's Work() or Inform() methods.
func ClientFromContext(ctx context.Context) *Client {
	client, err := ClientFromContextSafely(ctx)
	if err != nil {
		panic(err)
	}
	return client
}

// ClientFromContext returns the Client from the context. This function can
// only be used within a Controller's Work() or Inform() methods because that is the only place
// Delta sets the Client on the context.
//
// It returns an error if the context does not contain a Client, which will
// never happen from the context provided to a Controller's Work() or Inform() methods.
//
// See the examples for [ClientFromContext] to understand how to use this
// function.
func ClientFromContextSafely(ctx context.Context) (*Client, error) {
	client, exists := ctx.Value(contextKeyClient{}).(*Client)
	if !exists || client == nil {
		return nil, errClientNotInContext
	}
	return client, nil
}
