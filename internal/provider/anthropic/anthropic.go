// Package anthropic implements provider.Agent against the Anthropic API.
package anthropic

import (
	"context"

	"github.com/chibuike-kt/harmonia/internal/provider"
)

type Client struct {
	apiKey string
}

func New(apiKey string) *Client {
	return &Client{apiKey: apiKey}
}

func (c *Client) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	// TODO: wire to the real Anthropic Messages API. Left unimplemented
	// deliberately — this scaffold is structure, not a working client yet.
	panic("not implemented")
}
