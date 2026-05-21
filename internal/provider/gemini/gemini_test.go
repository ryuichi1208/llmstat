package gemini

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

const sampleSSE = `data: {"candidates":[{"content":{"parts":[{"text":"hello "}]}}]}

data: {"candidates":[{"content":{"parts":[{"text":"world"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}

`

func TestRun_AIStudio(t *testing.T) {
	var gotPath string
	var gotKey string
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.URL.Query().Get("key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sampleSSE)
	}))
	defer ts.Close()

	p := New(Config{APIKey: "test-key", BaseURL: ts.URL})
	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{
		Model:  "gemini-2.5-flash",
		Prompt: "hi",
	}, s, io.Discard)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(gotPath, "/models/gemini-2.5-flash:streamGenerateContent") {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("expected key=test-key, got %q", gotKey)
	}
	if gotAuth != "" {
		t.Errorf("AI Studio mode must not send Authorization header, got %q", gotAuth)
	}
	if s.InputTokens != 3 || s.Tokens != 2 || s.TotalTokens != 5 {
		t.Errorf("usage parse wrong: input=%d output=%d total=%d", s.InputTokens, s.Tokens, s.TotalTokens)
	}
}

func TestRun_Vertex(t *testing.T) {
	var gotPath string
	var gotKey string
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.URL.Query().Get("key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sampleSSE)
	}))
	defer ts.Close()

	p := New(Config{
		Region:      "us-central1",
		ProjectID:   "my-project",
		AccessToken: "tok",
		BaseURL:     ts.URL,
	})
	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{
		Model:  "gemini-2.5-pro",
		Prompt: "hi",
	}, s, io.Discard)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(gotPath, "/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-pro:streamGenerateContent") {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotKey != "" {
		t.Errorf("Vertex mode must not send API key query param, got %q", gotKey)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("expected Authorization=Bearer tok, got %q", gotAuth)
	}
}

func TestRun_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "forbidden")
	}))
	defer ts.Close()

	p := New(Config{APIKey: "k", BaseURL: ts.URL})
	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{Model: "m", Prompt: "p"}, s, io.Discard)
	if err == nil {
		t.Fatalf("expected error on HTTP 403")
	}
	if s.Status != http.StatusForbidden {
		t.Errorf("expected Status=403, got %d", s.Status)
	}
	if s.ErrorMsg == "" {
		t.Errorf("expected ErrorMsg to be set")
	}
}
