package cli

import "testing"

func mkEnv(m map[string]string) Env {
	return func(k string) string { return m[k] }
}

func TestSelectProvider(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		env  map[string]string
		want string
	}{
		{
			name: "explicit provider wins",
			opts: Options{Provider: "azure", Region: "us-central1"},
			want: "azure",
		},
		{
			name: "region selects gemini Vertex",
			opts: Options{Region: "us-central1"},
			want: "gemini",
		},
		{
			name: "AZURE_OPENAI_ENDPOINT env selects azure",
			env:  map[string]string{"AZURE_OPENAI_ENDPOINT": "https://x.openai.azure.com"},
			want: "azure",
		},
		{
			name: "azure-endpoint flag selects azure",
			opts: Options{AzureEndpoint: "https://x"},
			want: "azure",
		},
		{
			name: "bedrock-region flag selects bedrock",
			opts: Options{BedrockRegion: "us-east-1"},
			want: "bedrock",
		},
		{
			name: "AWS_REGION + anthropic model selects bedrock",
			opts: Options{Model: "anthropic.claude-3-5-sonnet-20241022-v2:0"},
			env:  map[string]string{"AWS_REGION": "us-east-1"},
			want: "bedrock",
		},
		{
			name: "AWS_REGION alone does NOT select bedrock",
			opts: Options{Model: "gemini-2.5-flash"},
			env:  map[string]string{"AWS_REGION": "us-east-1"},
			want: "gemini",
		},
		{
			name: "default is gemini",
			want: "gemini",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectProvider(&tc.opts, mkEnv(tc.env))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRun_NoPrompt(t *testing.T) {
	var stdout, stderr discardWriter
	code := Run([]string{}, mkEnv(nil), &stdout, &stderr, VersionInfo{Version: "test"})
	if code != 2 {
		t.Errorf("want exit 2 on missing prompt, got %d", code)
	}
}

func TestRun_Version(t *testing.T) {
	var stdout, stderr discardWriter
	code := Run([]string{"--version"}, mkEnv(nil), &stdout, &stderr, VersionInfo{Version: "1.2.3", Commit: "abc", Date: "today"})
	if code != 0 {
		t.Errorf("want exit 0 on --version, got %d (stderr=%s)", code, stderr.String())
	}
	if got := stdout.String(); got == "" {
		t.Errorf("want non-empty version output")
	}
}

type discardWriter struct{ buf []byte }

func (d *discardWriter) Write(p []byte) (int, error) {
	d.buf = append(d.buf, p...)
	return len(p), nil
}
func (d *discardWriter) String() string { return string(d.buf) }
