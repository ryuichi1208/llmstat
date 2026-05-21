// Package provider defines the LLM provider abstraction used by llmstat.
//
// Each provider implements Run, which performs a single streaming request
// against the underlying API while populating the shared Stats struct so the
// final httpstat-style render is the same across providers.
package provider

import (
	"context"
	"io"

	"github.com/ryuichi1208/llmstat/internal/stats"
)

type Provider interface {
	Name() string
	Run(ctx context.Context, req Request, s *stats.Stats, out io.Writer) error
}

type Request struct {
	Model       string
	Prompt      string
	MaxTokens   int
	Temperature float64

	// ThinkingBudget is gemini-specific. Providers that don't support it
	// should ignore the field.
	ThinkingBudget *int

	// Verbose causes the provider to print per-chunk progress to out as it
	// arrives. Final stats rendering happens at the call site.
	Verbose bool
}
