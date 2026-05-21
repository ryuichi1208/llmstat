package awssig

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// AWS SigV4 official test vector: get-vanilla
//
//	service: service
//	region:  us-east-1
//	access:  AKIDEXAMPLE
//	secret:  wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY
//	date:    20150830T123600Z
//
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4-signed-request-examples.html
func TestSigV4_GetVanilla(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"

	creds := Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	signingTime, _ := time.Parse("20060102T150405Z", "20150830T123600Z")

	canonical, _, signature, err := SignAndCanonical(req, nil, "service", "us-east-1", creds, signingTime, Options{
		IncludeContentSha256Header: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	wantCanonical := []string{
		"GET",
		"host:example.amazonaws.com",
		"x-amz-date:20150830T123600Z",
		"host;x-amz-date",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
	for _, s := range wantCanonical {
		if !strings.Contains(canonical, s) {
			t.Errorf("canonical missing %q\n--- canonical ---\n%s", s, canonical)
		}
	}

	const wantSig = "5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if signature != wantSig {
		t.Errorf("signature mismatch:\n  got:  %s\n  want: %s", signature, wantSig)
	}

	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request") {
		t.Errorf("Authorization header missing expected credential scope: %s", auth)
	}
	if !strings.Contains(auth, "Signature="+wantSig) {
		t.Errorf("Authorization header missing signature: %s", auth)
	}
}

func TestSigV4_BedrockMode_IncludesContentSha256(t *testing.T) {
	req, err := http.NewRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/model/x/invoke-with-response-stream", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	creds := Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		SessionToken:    "session-abc",
	}
	now, _ := time.Parse("20060102T150405Z", "20240101T000000Z")
	if err := Sign(req, []byte("{}"), "bedrock", "us-east-1", creds, now); err != nil {
		t.Fatal(err)
	}

	if got := req.Header.Get("X-Amz-Security-Token"); got != "session-abc" {
		t.Errorf("expected X-Amz-Security-Token=session-abc, got %q", got)
	}
	if got := req.Header.Get("X-Amz-Content-Sha256"); got == "" {
		t.Errorf("expected X-Amz-Content-Sha256 to be set in Bedrock mode")
	}
	if req.Header.Get("Authorization") == "" {
		t.Errorf("missing Authorization header")
	}
	if req.Header.Get("X-Amz-Date") != "20240101T000000Z" {
		t.Errorf("unexpected X-Amz-Date: %s", req.Header.Get("X-Amz-Date"))
	}
}

func TestCanonicalQueryString_SortedAndEncoded(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/?b=2&a=1&a=3", nil)
	creds := Credentials{AccessKeyID: "k", SecretAccessKey: "s"}
	now, _ := time.Parse("20060102T150405Z", "20240101T000000Z")
	canonical, _, _, err := SignAndCanonical(req, nil, "service", "us-east-1", creds, now, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(canonical, "\n")
	if len(lines) < 3 {
		t.Fatalf("canonical too short: %q", canonical)
	}
	if lines[2] != "a=1&a=3&b=2" {
		t.Errorf("canonical query line want %q, got %q", "a=1&a=3&b=2", lines[2])
	}
}
