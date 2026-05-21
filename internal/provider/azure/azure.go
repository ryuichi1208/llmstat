// Package azure implements the Azure OpenAI chat-completions streaming
// provider. The wire format is OpenAI-compatible (SSE with JSON chunks,
// terminated by "data: [DONE]").
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"github.com/ryuichi1208/llmstat/internal/provider"
	"github.com/ryuichi1208/llmstat/internal/sse"
	"github.com/ryuichi1208/llmstat/internal/stats"
)

type Config struct {
	Endpoint   string
	APIKey     string
	Deployment string
	APIVersion string
}

type Provider struct {
	cfg Config
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return "azure" }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type body struct {
	Messages      []message      `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Temperature   float64        `json:"temperature,omitempty"`
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *Provider) Run(ctx context.Context, req provider.Request, s *stats.Stats, out io.Writer) error {
	endpoint := strings.TrimRight(p.cfg.Endpoint, "/")
	urlStr := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		endpoint, p.cfg.Deployment, p.cfg.APIVersion)

	b := body{
		Messages: []message{{Role: "user", Content: req.Prompt}},
		Stream:   true,
		StreamOptions: &streamOptions{
			IncludeUsage: true,
		},
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}

	s.Provider = p.Name()
	s.Model = req.Model
	s.URL = urlStr
	s.ReqStart = time.Now()

	traceCtx := httptrace.WithClientTrace(ctx, stats.NewClientTrace(s))
	httpReq, err := http.NewRequestWithContext(traceCtx, http.MethodPost, urlStr, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("api-key", p.cfg.APIKey)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		s.EOF = time.Now()
		s.ErrorMsg = err.Error()
		return err
	}
	defer resp.Body.Close()

	s.Status = resp.StatusCode
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		s.EOF = time.Now()
		s.ErrorMsg = strings.TrimSpace(truncate(string(buf), 500))
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var apiErr string
	err = sse.Scan(resp.Body, func(ev sse.Event, ts time.Time) {
		if bytes.Equal(bytes.TrimSpace(ev.Data), []byte("[DONE]")) {
			return
		}
		if s.FirstChunk.IsZero() {
			s.FirstChunk = ts
		}
		s.LastChunk = ts
		s.ChunkTimes = append(s.ChunkTimes, ts)

		var c chunk
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			if req.Verbose {
				fmt.Fprintf(out, "  [chunk parse error: %v] raw=%s\n", err, truncate(string(ev.Data), 120))
			}
			return
		}
		if c.Error != nil {
			apiErr = fmt.Sprintf("%s: %s", c.Error.Code, c.Error.Message)
		}
		if c.Usage != nil {
			s.InputTokens = c.Usage.PromptTokens
			s.Tokens = c.Usage.CompletionTokens
			s.TotalTokens = c.Usage.TotalTokens
		}
		if req.Verbose {
			var text strings.Builder
			for _, ch := range c.Choices {
				text.WriteString(ch.Delta.Content)
			}
			elapsed := ts.Sub(s.ReqStart)
			fmt.Fprintf(out, "  [%6dms] %s\n",
				elapsed.Milliseconds(), truncate(text.String(), 80))
		}
	})
	s.EOF = time.Now()
	if err != nil {
		s.ErrorMsg = err.Error()
		return err
	}
	if s.Tokens == 0 {
		s.Tokens = len(s.ChunkTimes)
	}
	if apiErr != "" {
		s.ErrorMsg = apiErr
		return fmt.Errorf("%s", apiErr)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
