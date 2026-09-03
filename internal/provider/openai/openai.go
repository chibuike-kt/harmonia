// Package openai implements provider.Agent against the OpenAI API.
package openai

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
	// TODO: wire to the real OpenAI Chat Completions / Responses API.
	panic("not implemented")
}
