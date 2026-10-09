package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicBackend struct {
	model  string
	client anthropic.Client
}

// NewAnthropic returns a Backend over the official Anthropic SDK. opts are
// forwarded to anthropic.NewClient (tests pass option.WithBaseURL).
func NewAnthropic(model, apiKey string, opts ...option.RequestOption) Backend {
	all := append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &anthropicBackend{model: model, client: anthropic.NewClient(all...)}
}

func (a *anthropicBackend) Name() string  { return "anthropic" }
func (a *anthropicBackend) Model() string { return a.model }

func (a *anthropicBackend) Complete(ctx context.Context, req Request) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.User))},
	})
	if err != nil {
		return Response{}, fmt.Errorf("llm: anthropic: %w", err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return Response{}, fmt.Errorf("llm: anthropic refused the request (category %q)", resp.StopDetails.Category)
	}
	var sb strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return Response{
		Text: sb.String(),
		Usage: Usage{
			InputTokens:      int(resp.Usage.InputTokens),
			OutputTokens:     int(resp.Usage.OutputTokens),
			CacheReadTokens:  int(resp.Usage.CacheReadInputTokens),
			CacheWriteTokens: int(resp.Usage.CacheCreationInputTokens),
		},
		Model: string(resp.Model),
	}, nil
}
