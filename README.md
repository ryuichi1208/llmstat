# llmstat

`llmstat` は [httpstat](https://github.com/davecheney/httpstat) 風に **LLM ストリーミング API の遅延を可視化する** Go 製 CLI。

DNS / TCP / TLS / TTFT / Generation / Tail を 1 枚のバーで見せつつ、SSE / event stream の **chunk 間レイテンシ (ITL) の p50 / p95 / max** と **トークンスループット** を表示する。

サポート対象:

- **Gemini AI Studio** (API key)
- **Gemini Vertex AI** (GCP Service Account)
- **Azure OpenAI** (chat completions, SSE)
- **AWS Bedrock** (Anthropic on Bedrock, event stream)

## Install

```sh
go install github.com/ryuichi1208/llmstat/cmd/llmstat@latest
```

GitHub Releases にはクロスコンパイル済みバイナリ (linux / darwin × amd64 / arm64) が tag ごとに置かれる。

## Usage

```sh
llmstat [flags] "<prompt>"
```

Provider はフラグと環境変数から自動判定される。`--provider` で明示指定もできる。

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

共通:

| flag | env | 説明 |
|---|---|---|
| `--provider` |  | `gemini` / `azure` / `bedrock` を明示 |
| `-m`, `--model` |  | モデル名 |
| `--max-tokens` |  | max output tokens |
| `--temperature` |  | 温度 |
| `-v`, `--verbose` |  | chunk ごとの timing を出力 |
| `--version` |  | バージョン表示 |

Gemini:

| flag | env | 説明 |
|---|---|---|
| `-k`, `--api-key` | `GEMINI_API_KEY` | AI Studio の API key |
| `--region` |  | Vertex AI のリージョン (例: `us-central1`)。指定すると Vertex モード |
| `--project` |  | GCP project ID。未指定なら credentials JSON から解決 |
| `--credentials` | `GOOGLE_APPLICATION_CREDENTIALS` | Service Account JSON |
| `--thinking-budget` |  | gemini の thinkingBudget (0=off, -1=auto, N=cap) |

Azure:

| flag | env | 説明 |
|---|---|---|
| `--azure-endpoint` | `AZURE_OPENAI_ENDPOINT` | `https://<resource>.openai.azure.com` |
| `--azure-api-key` | `AZURE_OPENAI_API_KEY` | リソース api-key |
| `--azure-deployment` | `AZURE_OPENAI_DEPLOYMENT` | デプロイメント名 |
| `--azure-api-version` | `AZURE_OPENAI_API_VERSION` | デフォルト `2024-10-21` |

Bedrock:

| flag | env | 説明 |
|---|---|---|
| `--bedrock-region` | `AWS_REGION` | Bedrock リージョン |
|  | `AWS_ACCESS_KEY_ID` | アクセスキー |
|  | `AWS_SECRET_ACCESS_KEY` | シークレットキー |
|  | `AWS_SESSION_TOKEN` | (任意) セッショントークン |

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

`main` への push をトリガに自動でリリースが走る:

1. `auto-tag.yaml` が直近の `vX.Y.Z` を見て **patch を 1 つ進めた tag** を打ち、push する
2. tag push を受けて `release.yaml` が [GoReleaser](https://goreleaser.com/) を起動し、linux / darwin × amd64 / arm64 のバイナリを GitHub Releases に公開する

メジャー/マイナーを上げたい場合は手動で `git tag vX.Y.0 && git push origin vX.Y.0` する。

## License

MIT
