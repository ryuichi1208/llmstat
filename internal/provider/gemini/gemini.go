// Package gemini implements the Gemini API provider for both AI Studio
// (API-key auth) and Vertex AI (Service Account OAuth) modes.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/ryuichi1208/llmstat/internal/provider"
	"github.com/ryuichi1208/llmstat/internal/sse"
	"github.com/ryuichi1208/llmstat/internal/stats"
)

const (
	AIStudioBase = "https://generativelanguage.googleapis.com/v1beta"
	VertexScope  = "https://www.googleapis.com/auth/cloud-platform"
)

// Config carries already-resolved settings. The CLI layer decides which
// fields to fill: APIKey for AI Studio mode, or AccessToken + Region +
// ProjectID for Vertex mode.
type Config struct {
	APIKey string // AI Studio

	Region      string // Vertex
	ProjectID   string // Vertex
	AccessToken string // Vertex (already obtained via Service Account)

	// BaseURL overrides the API base (used in tests). Empty means default.
	BaseURL string
}

type Provider struct {
	cfg Config
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return "gemini" }

func (p *Provider) useVertex() bool {
	return p.cfg.Region != "" && p.cfg.AccessToken != ""
}

type body struct {
	Contents         []content         `json:"contents"`
	GenerationConfig *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	MaxOutputTokens int             `json:"maxOutputTokens,omitempty"`
	Temperature     float64         `json:"temperature,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type chunk struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

func (p *Provider) endpoint(model string) (*url.URL, error) {
	if p.useVertex() {
		base := p.cfg.BaseURL
		if base == "" {
			base = fmt.Sprintf("https://%s-aiplatform.googleapis.com", p.cfg.Region)
		}
		s := fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent",
			base,
			url.PathEscape(p.cfg.ProjectID),
			p.cfg.Region,
			url.PathEscape(model),
		)
		u, err := url.Parse(s)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("alt", "sse")
		u.RawQuery = q.Encode()
		return u, nil
	}
	base := p.cfg.BaseURL
	if base == "" {
		base = AIStudioBase
	}
	s := fmt.Sprintf("%s/models/%s:streamGenerateContent", base, url.PathEscape(model))
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("alt", "sse")
	q.Set("key", p.cfg.APIKey)
	u.RawQuery = q.Encode()
	return u, nil
}

func (p *Provider) Run(ctx context.Context, req provider.Request, s *stats.Stats, out io.Writer) error {
	u, err := p.endpoint(req.Model)
	if err != nil {
		return err
	}

	b := body{
		Contents: []content{{
			Role:  "user",
			Parts: []part{{Text: req.Prompt}},
		}},
	}
	if req.MaxTokens > 0 || req.Temperature > 0 || req.ThinkingBudget != nil {
		b.GenerationConfig = &generationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
		}
		if req.ThinkingBudget != nil {
			b.GenerationConfig.ThinkingConfig = &thinkingConfig{
				ThinkingBudget: *req.ThinkingBudget,
			}
		}
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}

	s.Provider = p.Name()
	s.Model = req.Model
	s.URL = u.Redacted()
	s.ReqStart = time.Now()

	traceCtx := httptrace.WithClientTrace(ctx, stats.NewClientTrace(s))
	httpReq, err := http.NewRequestWithContext(traceCtx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.useVertex() {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.AccessToken)
	}

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
			apiErr = fmt.Sprintf("%s: %s", c.Error.Status, c.Error.Message)
		}
		if c.UsageMetadata != nil {
			um := c.UsageMetadata
			if um.CandidatesTokenCount > 0 {
				s.Tokens = um.CandidatesTokenCount
			}
			if um.PromptTokenCount > 0 {
				s.InputTokens = um.PromptTokenCount
			}
			if um.ThoughtsTokenCount > 0 {
				s.ThinkTokens = um.ThoughtsTokenCount
			}
			if um.TotalTokenCount > 0 {
				s.TotalTokens = um.TotalTokenCount
			}
		}
		if req.Verbose {
			var text strings.Builder
			for _, cand := range c.Candidates {
				for _, p := range cand.Content.Parts {
					text.WriteString(p.Text)
				}
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
