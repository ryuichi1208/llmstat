# llmstat

`llmstat` is a Go CLI that **visualizes the latency of streaming LLM APIs** in the style of [httpstat](https://github.com/davecheney/httpstat).

It renders DNS / TCP / TLS / TTFT / Generation / Tail in a single bar, alongside **p50 / p95 / max of inter-chunk latency (ITL)** for SSE / event streams and **token throughput**.

Supported providers:

- **Gemini AI Studio** (API key)
- **Gemini Vertex AI** (GCP Service Account)
- **Azure OpenAI** (chat completions, SSE)
- **AWS Bedrock** (Anthropic on Bedrock, event stream)

## Install

```sh
go install github.com/ryuichi1208/llmstat/cmd/llmstat@latest
```

Cross-compiled binaries (linux / darwin × amd64 / arm64) are published to GitHub Releases for every tag.

## Usage

```sh
llmstat [flags] "<prompt>"
```

The provider is auto-detected from flags and environment variables. You can also select it explicitly with `--provider`.

### Gemini (AI Studio)

```sh
export GEMINI_API_KEY=...
llmstat -m gemini-2.5-flash "hello"
```

### Gemini (Vertex AI)

```sh
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json
llmstat --region us-central1 -m gemini-2.5-pro "hello"
```

### Azure OpenAI

```sh
export AZURE_OPENAI_ENDPOINT=https://<resource>.openai.azure.com
export AZURE_OPENAI_API_KEY=...
export AZURE_OPENAI_DEPLOYMENT=gpt-4o-mini
llmstat "hello"
```

### AWS Bedrock (Anthropic)

```sh
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
llmstat -m anthropic.claude-3-5-sonnet-20241022-v2:0 "hello"
```

## Flags

Common:

| flag | env | description |
|---|---|---|
| `--provider` |  | Explicitly select `gemini` / `azure` / `bedrock` |
| `-m`, `--model` |  | Model name |
| `--max-tokens` |  | Max output tokens |
| `--temperature` |  | Sampling temperature |
| `-v`, `--verbose` |  | Print per-chunk timing |
| `--version` |  | Print version |

Gemini:

| flag | env | description |
|---|---|---|
| `-k`, `--api-key` | `GEMINI_API_KEY` | AI Studio API key |
| `--region` |  | Vertex AI region (e.g. `us-central1`). Setting this enables Vertex mode |
| `--project` |  | GCP project ID. Resolved from the credentials JSON if omitted |
| `--credentials` | `GOOGLE_APPLICATION_CREDENTIALS` | Service Account JSON |
| `--thinking-budget` |  | Gemini `thinkingBudget` (0=off, -1=auto, N=cap) |

Azure:

| flag | env | description |
|---|---|---|
| `--azure-endpoint` | `AZURE_OPENAI_ENDPOINT` | `https://<resource>.openai.azure.com` |
| `--azure-api-key` | `AZURE_OPENAI_API_KEY` | Resource api-key |
| `--azure-deployment` | `AZURE_OPENAI_DEPLOYMENT` | Deployment name |
| `--azure-api-version` | `AZURE_OPENAI_API_VERSION` | Defaults to `2024-10-21` |

Bedrock:

| flag | env | description |
|---|---|---|
| `--bedrock-region` | `AWS_REGION` | Bedrock region |
|  | `AWS_ACCESS_KEY_ID` | Access key |
|  | `AWS_SECRET_ACCESS_KEY` | Secret key |
|  | `AWS_SESSION_TOKEN` | (Optional) session token |

## Output example

```
  DNS Lookup   TCP Connection  TLS Handshake  TTFT (Server Think)   Generation    Tail
[    12ms    |      45ms      |     78ms     |       320ms         |    420ms    | 5ms ]

  namelookup:12ms
      connect:57ms
       tls:135ms
         ttft:455ms
      gen_end:875ms
        total:880ms

Stream stats:
  provider:      gemini
  input tokens:  6
  output tokens: 38
  total tokens:  44
  first chunk:   320ms      (TTFT)
  inter-chunk:   p50=11ms  p95=29ms  max=53ms  (n=37)
  throughput:    90.5 tok/s
  tail:          5ms       (last chunk → conn close)
  model:         gemini-2.5-flash
  status:        200
```

## Release

A release is cut automatically on every push to `main`:

1. `auto-tag.yaml` looks up the most recent `vX.Y.Z` tag, **bumps the patch by one**, and pushes the new tag.
2. The tag push triggers `release.yaml`, which runs [GoReleaser](https://goreleaser.com/) and publishes linux / darwin × amd64 / arm64 binaries to GitHub Releases.

To bump major or minor, push the tag manually: `git tag vX.Y.0 && git push origin vX.Y.0`.

## License

MIT
