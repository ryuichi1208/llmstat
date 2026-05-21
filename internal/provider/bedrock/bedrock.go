// Package bedrock implements the AWS Bedrock streaming provider. Targets
// Anthropic-on-Bedrock models. Authenticates with SigV4 and decodes the
// vnd.amazon.eventstream response.
package bedrock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/ryuichi1208/llmstat/internal/awssig"
	"github.com/ryuichi1208/llmstat/internal/awsstream"
	"github.com/ryuichi1208/llmstat/internal/provider"
	"github.com/ryuichi1208/llmstat/internal/stats"
)

type Config struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	// Endpoint overrides the default Bedrock runtime endpoint (used in tests).
	Endpoint string
	// Now overrides time.Now for stable SigV4 timestamps in tests.
	Now func() time.Time
}

type Provider struct {
	cfg Config
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return "bedrock" }

type anthropicBody struct {
	AnthropicVersion string             `json:"anthropic_version"`
	Messages         []anthropicMessage `json:"messages"`
	MaxTokens        int                `json:"max_tokens"`
	Temperature      *float64           `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chunkOuter is the wrapper bedrock places around each frame payload.
type chunkOuter struct {
	Bytes string `json:"bytes"`
}

// chunkInner is the anthropic streaming event JSON inside the base64 bytes.
type chunkInner struct {
	Type  string `json:"type"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Message *struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *Provider) Run(ctx context.Context, req provider.Request, s *stats.Stats, out io.Writer) error {
	base := p.cfg.Endpoint
	if base == "" {
		base = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", p.cfg.Region)
	}
	urlStr := fmt.Sprintf("%s/model/%s/invoke-with-response-stream", base, url.PathEscape(req.Model))

	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	body := anthropicBody{
		AnthropicVersion: "bedrock-2023-05-31",
		Messages: []anthropicMessage{
			{Role: "user", Content: req.Prompt},
		},
		MaxTokens: maxTok,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		body.Temperature = &t
	}
	payload, err := json.Marshal(body)
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
	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")

	now := time.Now
	if p.cfg.Now != nil {
		now = p.cfg.Now
	}
	creds := awssig.Credentials{
		AccessKeyID:     p.cfg.AccessKeyID,
		SecretAccessKey: p.cfg.SecretAccessKey,
		SessionToken:    p.cfg.SessionToken,
	}
	if err := awssig.Sign(httpReq, payload, "bedrock", p.cfg.Region, creds, now()); err != nil {
		return fmt.Errorf("SigV4 sign failed: %w", err)
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

	dec := awsstream.NewDecoder(resp.Body)
	for {
		msg, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.EOF = time.Now()
			s.ErrorMsg = err.Error()
			return err
		}
		ts := time.Now()

		eventType := msg.Header(":event-type")
		messageType := msg.Header(":message-type")
		if messageType == "exception" || strings.Contains(strings.ToLower(eventType), "exception") {
			s.EOF = time.Now()
			s.ErrorMsg = strings.TrimSpace(truncate(string(msg.Payload), 500))
			return fmt.Errorf("bedrock exception: %s", eventType)
		}

		if s.FirstChunk.IsZero() {
			s.FirstChunk = ts
		}
		s.LastChunk = ts
		s.ChunkTimes = append(s.ChunkTimes, ts)

		var outer chunkOuter
		if err := json.Unmarshal(msg.Payload, &outer); err != nil {
			if req.Verbose {
				fmt.Fprintf(out, "  [chunk outer parse error: %v] raw=%s\n", err, truncate(string(msg.Payload), 120))
			}
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(outer.Bytes)
		if err != nil {
			if req.Verbose {
				fmt.Fprintf(out, "  [chunk b64 decode error: %v]\n", err)
			}
			continue
		}
		var inner chunkInner
		if err := json.Unmarshal(raw, &inner); err != nil {
			if req.Verbose {
				fmt.Fprintf(out, "  [chunk inner parse error: %v] raw=%s\n", err, truncate(string(raw), 120))
			}
			continue
		}
		if inner.Message != nil && inner.Message.Usage != nil {
			s.InputTokens = inner.Message.Usage.InputTokens
			s.Tokens = inner.Message.Usage.OutputTokens
		}
		if inner.Usage != nil {
			if inner.Usage.InputTokens > 0 {
				s.InputTokens = inner.Usage.InputTokens
			}
			if inner.Usage.OutputTokens > 0 {
				s.Tokens = inner.Usage.OutputTokens
			}
		}
		if req.Verbose && inner.Delta != nil {
			elapsed := ts.Sub(s.ReqStart)
			fmt.Fprintf(out, "  [%6dms] %s\n", elapsed.Milliseconds(), truncate(inner.Delta.Text, 80))
		}
	}
	s.EOF = time.Now()
	if s.InputTokens > 0 || s.Tokens > 0 {
		s.TotalTokens = s.InputTokens + s.Tokens
	}
	if s.Tokens == 0 {
		s.Tokens = len(s.ChunkTimes)
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
