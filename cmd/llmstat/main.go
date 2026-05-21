package main

import (
	"os"

	"github.com/ryuichi1208/llmstat/internal/cli"
)

func main() {
	os.Exit(cli.Run(
		os.Args[1:],
		os.Getenv,
		os.Stdout,
		os.Stderr,
		cli.VersionInfo{Version: version, Commit: commit, Date: date},
	))
}
