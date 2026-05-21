# llmstat: マルチプロバイダ対応 + tagpr 配布 設計

- 作成日: 2026-05-21
- ステータス: ドラフト
- 対象リポジトリ: `github.com/ryuichi1208/llmstat`

## Context

`llmstat` は httpstat 風に LLM ストリーミング API の遅延を可視化する CLI。すでに Gemini (AI Studio / Vertex AI) 向けの実装スケッチがある状態で、これを正式にリポジトリへ取り込みつつ、以下の 2 点を満たす形に組み直す:

1. **マルチプロバイダ化**: Gemini に加えて Azure OpenAI、AWS Bedrock もサポートする。provider が増えても httpstat 風の出力（DNS / TCP / TLS / TTFT / Generation / Tail）と `inter-chunk` 統計は同じフォーマットで出せるようにする。
2. **配布の自動化**: tagpr で Release PR を回し、tag push をトリガに GoReleaser がクロスコンパイル済みバイナリを GitHub Releases に公開する。

リポジトリは現状空（git init のみ）で、コミットも `main` 以外のブランチも存在しない。greenfield として最初のディレクトリ構成から固めるのが目的。

## 非目標

- provider のレスポンス本文の品質評価・正答率測定（あくまで遅延可視化ツール）
- chat 履歴の永続化、会話継続
- Bedrock 上の Anthropic 以外のモデル（Llama, Cohere など）への初版対応（将来拡張で対応可能な構造にだけする）
- Homebrew tap のリポジトリ作成自体（GoReleaser 設定は入れるがリポジトリ作成は本作業外）

## アーキテクチャ

### ディレクトリ構成

```
.
├── cmd/llmstat/                # main: CLI エントリポイント
│   ├── main.go
│   └── version.go              # ldflags で埋め込む version
├── internal/
│   ├── cli/                    # フラグ定義、provider セレクタ、env 解決
│   │   └── cli.go
│   ├── stats/                  # Stats 構造体、httptrace フック、Render
│   │   ├── stats.go
│   │   ├── render.go
│   │   └── stats_test.go
│   ├── sse/                    # SSE スキャナ（provider非依存）
│   │   ├── sse.go
│   │   └── sse_test.go
│   ├── awssig/                 # Bedrock 用 SigV4 署名（最小実装）
│   │   ├── sigv4.go
│   │   └── sigv4_test.go
│   ├── awsstream/              # AWS event stream フレームデコーダ
│   │   ├── frame.go
│   │   └── frame_test.go
│   └── provider/
│       ├── provider.go         # Provider interface, Request 構造体
│       ├── gemini/             # AI Studio + Vertex AI
│       │   ├── gemini.go
│       │   ├── auth.go
│       │   └── gemini_test.go
│       ├── azure/              # Azure OpenAI chat completions (SSE)
│       │   ├── azure.go
│       │   └── azure_test.go
│       └── bedrock/            # Bedrock InvokeModelWithResponseStream
│           ├── bedrock.go
│           └── bedrock_test.go
├── docs/superpowers/specs/     # 本ドキュメント
├── .github/workflows/
│   ├── tagpr.yaml
│   ├── release.yaml
│   └── ci.yaml                 # go test + go vet（main push と PR）
├── .tagpr
├── .goreleaser.yaml
├── go.mod
├── README.md
└── LICENSE
```

### モジュールパス・バイナリ名

- Module: `github.com/ryuichi1208/llmstat`
- バイナリ: `llmstat` (`go install github.com/ryuichi1208/llmstat/cmd/llmstat@latest` で入る)

### 核となる interface

`internal/provider/provider.go`:

```go
package provider

import (
    "context"
    "io"

    "github.com/ryuichi1208/llmstat/internal/stats"
)

type Provider interface {
    Name() string
    Run(ctx context.Context, req Request, s *stats.Stats, out io.Writer) error
}

type Request struct {
    Model          string
    Prompt         string
    MaxTokens      int
    Temperature    float64
    ThinkingBudget *int   // gemini 専用。他 provider は無視
    Verbose        bool
}
```

provider 固有の設定（API key、endpoint、region、deployment、credentials path 等）は **各 provider のコンストラクタ引数** として渡す。`Request` は LLM 入出力の本質パラメータだけを持つ。

### 共通 Stats

`internal/stats/stats.go` は既存スケッチの `Stats` 構造体をベースに、`AuthDuration` を「provider 共通の前処理時間」として残す（Gemini Vertex は JWT 交換、Bedrock は SigV4 計算自体は速いので 0 のままで OK、Azure は 0）。

`computeITL`（p50/p95/max の inter-chunk latency）と `Render`（httpstat 風カラム）は provider 横断で 1 つ。

### Provider 別の中身

**gemini パッケージ**

- 既存の `gemini.go` / `auth.go` をほぼ移植。AI Studio モード（API key）と Vertex モード（Service Account JWT → access token）を 1 つの `Provider` 実装に統合する。
- CLI 側で `--region` の有無を見てモードを決定し、`NewProvider(cfg)` に解決済み設定（API key または access token + project + region）を渡す。
- thinking budget は Gemini 固有なので、`Request.ThinkingBudget` を見る provider はこれだけ。

**azure パッケージ**

- エンドポイント: `{AZURE_OPENAI_ENDPOINT}/openai/deployments/{deployment}/chat/completions?api-version={apiVersion}` (デフォルト `2024-10-21`)
- ヘッダ: `api-key: {key}`、`Content-Type: application/json`、`Accept: text/event-stream`
- body: OpenAI 互換 (`messages`, `stream: true`, `max_tokens`, `temperature`, `stream_options: {include_usage: true}`)
- レスポンス: SSE。`data: [DONE]` を終端マーカーとして無視。各 `data:` を JSON parse し `choices[].delta.content` を chunk テキストに集め、最終チャンクの `usage` から token を取り出す。
- `internal/sse` をそのまま使える。

**bedrock パッケージ**

- エンドポイント: `https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/invoke-with-response-stream`
- 認証: SigV4 (`internal/awssig`)。サービス名は `bedrock`、リージョンは `--bedrock-region` または `AWS_REGION`。
- レスポンス: **AWS event stream** (`application/vnd.amazon.eventstream`)。SSE ではないため `internal/awsstream` のフレームデコーダを通し、`event-type: chunk` のペイロード JSON を取り出して provider 側でデコードする。
- 初版ターゲットは Anthropic on Bedrock (`anthropic.claude-*`)。body は `{anthropic_version, messages, max_tokens}`。chunk 内の `delta.text`、最終 `message_stop` の `usage` を読む。

### CLI と env 解決

`cmd/llmstat/main.go` は薄く保ち、`internal/cli` の `Run(args, env, stdout, stderr)` を呼ぶだけ。`internal/cli` は次の責務を持つ:

1. フラグ・env の解釈
2. provider 自動判定
3. provider 構築 → `provider.Run` 呼び出し
4. `stats.Render(stdout)` の最終出力

**provider 自動判定（先勝ち）:**

1. `--provider` 明示指定があればそれ
2. `--region`（Gemini 用）ありなら `gemini` (Vertex モード)
3. `AZURE_OPENAI_ENDPOINT` がセットされていれば `azure`
4. `--bedrock-region` か（`AWS_REGION` セット済み かつ `--model` が `anthropic.` で始まる）なら `bedrock`
5. デフォルト: `gemini` (AI Studio)

**主なフラグ:**

- 共通: `--provider`, `-m/--model`, `--max-tokens`, `--temperature`, `-v/--verbose`, `--version`
- gemini: `-k/--api-key`, `--region`, `--project`, `--credentials`, `--thinking-budget`
- azure: `--azure-endpoint`, `--azure-deployment`, `--azure-api-version`, `--azure-api-key`
- bedrock: `--bedrock-region`

**主な env:**

- `GEMINI_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`
- `AZURE_OPENAI_ENDPOINT`, `AZURE_OPENAI_API_KEY`, `AZURE_OPENAI_DEPLOYMENT`, `AZURE_OPENAI_API_VERSION`
- `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`

## テスト方針

- `internal/sse`: 既存のテーブルテストを移植
- `internal/stats`: `computeITL` の境界ケース（空、要素 1 個、要素 2 個）
- `internal/awssig`: AWS 公式テストベクター (`get-vanilla` 系) の 1〜2 ケースで Canonical Request と Signature の一致を確認
- `internal/awsstream`: 既知バイト列のフレームをデコードできることを確認
- 各 provider: `httptest.Server` でフェイクのストリーミングレスポンスを返し、
  - `Stats` の `FirstChunk`, `LastChunk`, `Tokens`, `InputTokens` が埋まる
  - 抽出テキストが期待値と一致する
  - HTTP 4xx で `ErrorMsg` が入って error が返る
- `internal/cli`: provider 自動判定のテーブルテスト（env と flag の組み合わせ）

CI は `go vet ./...` + `go test ./...` + `gofmt -l .`（差分があれば fail）を最低限走らせる。

## tagpr + GoReleaser 構成

### `.tagpr`

```
[tagpr]
releaseBranch = main
versionFile   = 
```

versionFile を空にすることで、tagpr は git tag 自体をバージョン源とする。Go の semver tag (`v0.1.0`) を打つ運用。

### `.goreleaser.yaml`

要点:
- `builds`: `main: ./cmd/llmstat`、`ldflags: -s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}`
- `goos`: linux, darwin, windows / `goarch`: amd64, arm64 (windows は amd64 のみ)
- `archives`: tar.gz (linux/darwin)、zip (windows)、checksums
- `brews`: 任意セクション（tap repo 未作成の場合はコメントアウトしておき、後日有効化）
- `release.draft: false`、`release.prerelease: auto`

### `.github/workflows/tagpr.yaml`

トリガ: `push: branches: [main]`、`permissions: contents: write, pull-requests: write`
ステップ: `actions/checkout@v4` → `Songmu/tagpr@v1`

### `.github/workflows/release.yaml`

トリガ: `push: tags: ['v*']`、`permissions: contents: write`
ステップ: checkout (fetch-depth: 0) → `actions/setup-go@v5` → `goreleaser/goreleaser-action@v6 with: args: release --clean`、`env: GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`

### `.github/workflows/ci.yaml`

トリガ: `push: branches: [main]`、`pull_request:`
ステップ: setup-go → `go vet ./...` → `go test -race ./...` → `gofmt -l .` (差分 fail)

### バージョン埋め込み

`cmd/llmstat/version.go`:

```go
package main

var (
    version = "dev"
    commit  = "none"
    date    = "unknown"
)
```

`--version` フラグでこの 3 値を表示。

## 検証

ローカル:

1. `go build ./...` がエラーなく通る
2. `go test ./...` が緑
3. `go run ./cmd/llmstat "hello"` で Gemini AI Studio モード（`GEMINI_API_KEY` 設定済み環境）が動き、httpstat 風出力が出る
4. `go run ./cmd/llmstat --provider azure -m gpt-4o-mini "hello"` で Azure モードが動く（要 env）
5. `go run ./cmd/llmstat --provider bedrock --bedrock-region us-east-1 -m anthropic.claude-3-5-sonnet-20241022-v2:0 "hello"` で Bedrock が動く（要 AWS 資格情報）
6. `goreleaser release --snapshot --clean --skip=publish` でローカルクロスビルドが通る

CI:

7. push to main → tagpr が Release PR を作る/更新する
8. Release PR を merge → tag が打たれ、release.yaml が GoReleaser を走らせ Releases にアセットが並ぶ
