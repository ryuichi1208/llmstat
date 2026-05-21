package azure

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryuichi1208/llmstat/internal/provider"
	"github.com/ryuichi1208/llmstat/internal/stats"
)

const sampleSSE = `data: {"choices":[{"delta":{"content":"hello "},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":"world"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}

data: [DONE]

`

func TestRun_Azure(t *testing.T) {
	var gotPath, gotAPIKey, gotAPIVersion string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("api-key")
		gotAPIVersion = r.URL.Query().Get("api-version")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sampleSSE)
	}))
	defer ts.Close()

	p := New(Config{
		Endpoint:   ts.URL,
		APIKey:     "az-key",
		Deployment: "gpt-4o-mini",
		APIVersion: "2024-10-21",
	})
	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{
		Model:  "gpt-4o-mini",
		Prompt: "hi",
	}, s, io.Discard)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(gotPath, "/openai/deployments/gpt-4o-mini/chat/completions") {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotAPIKey != "az-key" {
		t.Errorf("expected api-key=az-key, got %q", gotAPIKey)
	}
	if gotAPIVersion != "2024-10-21" {
		t.Errorf("expected api-version=2024-10-21, got %q", gotAPIVersion)
	}
	if s.InputTokens != 3 || s.Tokens != 2 || s.TotalTokens != 5 {
		t.Errorf("usage parse wrong: input=%d output=%d total=%d", s.InputTokens, s.Tokens, s.TotalTokens)
	}
	// [DONE] should not be counted as a chunk
	if len(s.ChunkTimes) != 3 {
		t.Errorf("expected 3 chunks (excluding [DONE]), got %d", len(s.ChunkTimes))
	}
}

func TestRun_AzureHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"401","message":"unauthorized"}}`)
	}))
	defer ts.Close()

	p := New(Config{
		Endpoint: ts.URL, APIKey: "x", Deployment: "d", APIVersion: "2024-10-21",
	})
	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{Model: "m", Prompt: "p"}, s, io.Discard)
	if err == nil {
		t.Fatalf("expected error on HTTP 401")
	}
	if s.Status != http.StatusUnauthorized {
		t.Errorf("expected Status=401, got %d", s.Status)
	}
}
