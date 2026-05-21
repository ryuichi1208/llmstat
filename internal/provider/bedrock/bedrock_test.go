package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryuichi1208/llmstat/internal/awsstream"
	"github.com/ryuichi1208/llmstat/internal/provider"
	"github.com/ryuichi1208/llmstat/internal/stats"
)

func wrapAnthropic(t *testing.T, inner any) []byte {
	t.Helper()
	raw, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	outer := map[string]string{"bytes": base64.StdEncoding.EncodeToString(raw)}
	b, err := json.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRun_Bedrock(t *testing.T) {
	var gotAuth string
	var gotXAmzDate string
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotXAmzDate = r.Header.Get("X-Amz-Date")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")

		// message_start with usage prelude
		w.Write(awsstream.EncodeFrame(
			[]awsstream.Header{{Name: ":event-type", Value: "chunk"}, {Name: ":content-type", Value: "application/json"}},
			wrapAnthropic(t, map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"usage": map[string]any{"input_tokens": 5, "output_tokens": 0},
				},
			}),
		))
		// two content deltas
		w.Write(awsstream.EncodeFrame(
			[]awsstream.Header{{Name: ":event-type", Value: "chunk"}},
			wrapAnthropic(t, map[string]any{
				"type":  "content_block_delta",
				"delta": map[string]any{"type": "text_delta", "text": "hello "},
			}),
		))
		w.Write(awsstream.EncodeFrame(
			[]awsstream.Header{{Name: ":event-type", Value: "chunk"}},
			wrapAnthropic(t, map[string]any{
				"type":  "content_block_delta",
				"delta": map[string]any{"type": "text_delta", "text": "world"},
			}),
		))
		// message_delta with final usage
		w.Write(awsstream.EncodeFrame(
			[]awsstream.Header{{Name: ":event-type", Value: "chunk"}},
			wrapAnthropic(t, map[string]any{
				"type":  "message_delta",
				"usage": map[string]any{"input_tokens": 5, "output_tokens": 2},
			}),
		))
	}))
	defer ts.Close()

	p := New(Config{
		Region:          "us-east-1",
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Endpoint:        ts.URL,
		Now: func() time.Time {
			tt, _ := time.Parse("20060102T150405Z", "20240101T000000Z")
			return tt
		},
	})

	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{
		Model:  "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Prompt: "hi",
	}, s, io.Discard)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(gotPath, "/model/anthropic.claude-3-5-sonnet-20241022-v2:0/invoke-with-response-stream") {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if !strings.Contains(gotAuth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20240101/us-east-1/bedrock/aws4_request") {
		t.Errorf("Authorization header wrong: %s", gotAuth)
	}
	if gotXAmzDate != "20240101T000000Z" {
		t.Errorf("X-Amz-Date wrong: %s", gotXAmzDate)
	}
	if s.InputTokens != 5 || s.Tokens != 2 {
		t.Errorf("usage parse wrong: input=%d output=%d", s.InputTokens, s.Tokens)
	}
	if len(s.ChunkTimes) != 4 {
		t.Errorf("expected 4 chunks, got %d", len(s.ChunkTimes))
	}
}

func TestRun_BedrockHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"bad model"}`))
	}))
	defer ts.Close()

	p := New(Config{
		Region:          "us-east-1",
		AccessKeyID:     "k",
		SecretAccessKey: "s",
		Endpoint:        ts.URL,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
	})
	s := &stats.Stats{}
	err := p.Run(context.Background(), provider.Request{Model: "bad", Prompt: "p"}, s, io.Discard)
	if err == nil {
		t.Fatalf("expected error on HTTP 400")
	}
	if s.Status != http.StatusBadRequest {
		t.Errorf("expected Status=400, got %d", s.Status)
	}
}
