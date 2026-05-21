// Package cli wires command-line flags and environment variables into a
// concrete provider and runs a single LLM streaming request, then renders
// the collected stats.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ryuichi1208/llmstat/internal/provider"
	"github.com/ryuichi1208/llmstat/internal/provider/azure"
	"github.com/ryuichi1208/llmstat/internal/provider/bedrock"
	"github.com/ryuichi1208/llmstat/internal/provider/gemini"
	"github.com/ryuichi1208/llmstat/internal/stats"
)

type VersionInfo struct {
	Version string
	Commit  string
	Date    string
}

type Options struct {
	Provider       string
	Model          string
	MaxTokens      int
	Temperature    float64
	ThinkingBudget int
	Verbose        bool
	ShowVersion    bool

	// gemini
	APIKey      string
	Region      string
	Project     string
	Credentials string

	// azure
	AzureEndpoint   string
	AzureDeployment string
	AzureAPIKey     string
	AzureAPIVersion string

	// bedrock
	BedrockRegion string

	Prompt string
}

// Env reads a single environment variable; tests inject a fake.
type Env func(key string) string

func Run(args []string, env Env, stdout, stderr io.Writer, ver VersionInfo) int {
	if env == nil {
		env = func(string) string { return "" }
	}

	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if opts.ShowVersion {
		fmt.Fprintf(stdout, "llmstat %s (commit %s, built %s)\n", ver.Version, ver.Commit, ver.Date)
		return 0
	}

	if opts.Prompt == "" {
		fmt.Fprintln(stderr, "error: prompt が指定されていません")
		return 2
	}

	prov, req, s, err := buildProvider(opts, env)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	ctx := context.Background()
	runErr := prov.Run(ctx, req, s, stdout)

	if opts.Verbose {
		fmt.Fprintln(stdout)
	}
	s.Render(stdout)

	if runErr != nil {
		return 1
	}
	return 0
}

func parseFlags(args []string, stderr io.Writer) (*Options, error) {
	fs := flag.NewFlagSet("llmstat", flag.ContinueOnError)
	fs.SetOutput(stderr)

	opts := &Options{}
	fs.StringVar(&opts.Provider, "provider", "", "provider name (gemini, azure, bedrock). 未指定なら env/flag から自動判定")
	fs.StringVar(&opts.Model, "m", "", "model name")
	fs.StringVar(&opts.Model, "model", "", "model name")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "max output tokens (optional)")
	fs.Float64Var(&opts.Temperature, "temperature", 0, "temperature (optional)")
	fs.IntVar(&opts.ThinkingBudget, "thinking-budget", -2, "[gemini] thinkingBudget (0=off, -1=auto, N=cap). -2=unset")
	fs.BoolVar(&opts.Verbose, "v", false, "print each chunk timing")
	fs.BoolVar(&opts.Verbose, "verbose", false, "print each chunk timing")
	fs.BoolVar(&opts.ShowVersion, "version", false, "print version and exit")

	// gemini
	fs.StringVar(&opts.APIKey, "k", "", "[gemini] AI Studio API key (default $GEMINI_API_KEY)")
	fs.StringVar(&opts.APIKey, "api-key", "", "[gemini] AI Studio API key (default $GEMINI_API_KEY)")
	fs.StringVar(&opts.Region, "region", "", "[gemini] Vertex AI region (enables Vertex mode)")
	fs.StringVar(&opts.Project, "project", "", "[gemini] GCP project ID (default: project_id in credentials JSON)")
	fs.StringVar(&opts.Credentials, "credentials", "", "[gemini] service account JSON path (default $GOOGLE_APPLICATION_CREDENTIALS)")

	// azure
	fs.StringVar(&opts.AzureEndpoint, "azure-endpoint", "", "[azure] resource endpoint (default $AZURE_OPENAI_ENDPOINT)")
	fs.StringVar(&opts.AzureDeployment, "azure-deployment", "", "[azure] deployment name (default $AZURE_OPENAI_DEPLOYMENT)")
	fs.StringVar(&opts.AzureAPIKey, "azure-api-key", "", "[azure] api-key (default $AZURE_OPENAI_API_KEY)")
	fs.StringVar(&opts.AzureAPIVersion, "azure-api-version", "", "[azure] api version (default $AZURE_OPENAI_API_VERSION or 2024-10-21)")

	// bedrock
	fs.StringVar(&opts.BedrockRegion, "bedrock-region", "", "[bedrock] AWS region (default $AWS_REGION)")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: llmstat [flags] \"<prompt>\"")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "httpstat風にLLMストリーミングAPIの遅延を可視化する。")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	opts.Prompt = strings.TrimSpace(strings.Join(fs.Args(), " "))
	return opts, nil
}

// SelectProvider returns the chosen provider name based on flags + env.
// Precedence:
//  1. explicit --provider
//  2. --region (Vertex gemini)
//  3. AZURE_OPENAI_ENDPOINT or --azure-endpoint
//  4. --bedrock-region or (AWS_REGION env AND model starts with "anthropic.")
//  5. default: gemini
func SelectProvider(opts *Options, env Env) string {
	if opts.Provider != "" {
		return opts.Provider
	}
	if opts.Region != "" {
		return "gemini"
	}
	if opts.AzureEndpoint != "" || env("AZURE_OPENAI_ENDPOINT") != "" {
		return "azure"
	}
	if opts.BedrockRegion != "" {
		return "bedrock"
	}
	if env("AWS_REGION") != "" && strings.HasPrefix(opts.Model, "anthropic.") {
		return "bedrock"
	}
	return "gemini"
}

func buildProvider(opts *Options, env Env) (provider.Provider, provider.Request, *stats.Stats, error) {
	name := SelectProvider(opts, env)

	req := provider.Request{
		Model:       opts.Model,
		Prompt:      opts.Prompt,
		MaxTokens:   opts.MaxTokens,
		Temperature: opts.Temperature,
		Verbose:     opts.Verbose,
	}
	if opts.ThinkingBudget != -2 {
		b := opts.ThinkingBudget
		req.ThinkingBudget = &b
	}

	s := &stats.Stats{}

	switch name {
	case "gemini":
		cfg, authDur, err := buildGeminiConfig(opts, env)
		if err != nil {
			return nil, req, s, err
		}
		s.AuthDuration = authDur
		if req.Model == "" {
			req.Model = "gemini-2.5-flash"
		}
		return gemini.New(cfg), req, s, nil
	case "azure":
		cfg, err := buildAzureConfig(opts, env)
		if err != nil {
			return nil, req, s, err
		}
		if req.Model == "" {
			req.Model = cfg.Deployment
		}
		return azure.New(cfg), req, s, nil
	case "bedrock":
		cfg, err := buildBedrockConfig(opts, env)
		if err != nil {
			return nil, req, s, err
		}
		if req.Model == "" {
			return nil, req, s, fmt.Errorf("bedrock: -m/--model が必要 (例: anthropic.claude-3-5-sonnet-20241022-v2:0)")
		}
		return bedrock.New(cfg), req, s, nil
	default:
		return nil, req, s, fmt.Errorf("unknown provider %q", name)
	}
}

func buildGeminiConfig(opts *Options, env Env) (gemini.Config, time.Duration, error) {
	if opts.Region != "" {
		credPath := opts.Credentials
		if credPath == "" {
			credPath = env("GOOGLE_APPLICATION_CREDENTIALS")
		}
		if credPath == "" {
			return gemini.Config{}, 0, fmt.Errorf("vertex モードには credentials が必要です (--credentials または $GOOGLE_APPLICATION_CREDENTIALS)")
		}
		sa, err := gemini.LoadServiceAccount(credPath)
		if err != nil {
			return gemini.Config{}, 0, err
		}
		project := opts.Project
		if project == "" {
			project = sa.ProjectID
		}
		if project == "" {
			return gemini.Config{}, 0, fmt.Errorf("project ID が解決できません (--project を指定してください)")
		}
		authStart := time.Now()
		tok, err := sa.FetchAccessToken(gemini.VertexScope)
		authDur := time.Since(authStart)
		if err != nil {
			return gemini.Config{}, authDur, fmt.Errorf("access token 取得失敗: %w", err)
		}
		return gemini.Config{
			Region:      opts.Region,
			ProjectID:   project,
			AccessToken: tok,
		}, authDur, nil
	}

	key := opts.APIKey
	if key == "" {
		key = env("GEMINI_API_KEY")
	}
	if key == "" {
		return gemini.Config{}, 0, fmt.Errorf("GEMINI_API_KEY が未設定です (-k で渡すか --region でVertexモードへ)")
	}
	return gemini.Config{APIKey: key}, 0, nil
}

func buildAzureConfig(opts *Options, env Env) (azure.Config, error) {
	endpoint := opts.AzureEndpoint
	if endpoint == "" {
		endpoint = env("AZURE_OPENAI_ENDPOINT")
	}
	key := opts.AzureAPIKey
	if key == "" {
		key = env("AZURE_OPENAI_API_KEY")
	}
	deployment := opts.AzureDeployment
	if deployment == "" {
		deployment = env("AZURE_OPENAI_DEPLOYMENT")
	}
	apiVersion := opts.AzureAPIVersion
	if apiVersion == "" {
		apiVersion = env("AZURE_OPENAI_API_VERSION")
	}
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}
	if endpoint == "" {
		return azure.Config{}, fmt.Errorf("azure: endpoint が未設定 (--azure-endpoint or $AZURE_OPENAI_ENDPOINT)")
	}
	if key == "" {
		return azure.Config{}, fmt.Errorf("azure: api-key が未設定 (--azure-api-key or $AZURE_OPENAI_API_KEY)")
	}
	if deployment == "" {
		return azure.Config{}, fmt.Errorf("azure: deployment が未設定 (--azure-deployment or $AZURE_OPENAI_DEPLOYMENT)")
	}
	return azure.Config{
		Endpoint:   endpoint,
		APIKey:     key,
		Deployment: deployment,
		APIVersion: apiVersion,
	}, nil
}

func buildBedrockConfig(opts *Options, env Env) (bedrock.Config, error) {
	region := opts.BedrockRegion
	if region == "" {
		region = env("AWS_REGION")
	}
	if region == "" {
		return bedrock.Config{}, fmt.Errorf("bedrock: region が未設定 (--bedrock-region or $AWS_REGION)")
	}
	keyID := env("AWS_ACCESS_KEY_ID")
	secret := env("AWS_SECRET_ACCESS_KEY")
	if keyID == "" || secret == "" {
		return bedrock.Config{}, fmt.Errorf("bedrock: AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY が必要")
	}
	return bedrock.Config{
		Region:          region,
		AccessKeyID:     keyID,
		SecretAccessKey: secret,
		SessionToken:    env("AWS_SESSION_TOKEN"),
	}, nil
}
