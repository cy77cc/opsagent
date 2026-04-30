package main

import (
	"os"

	"github.com/cy77cc/opsagent/internal/app"
)

// These variables are set at build time via ldflags.
var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

func main() {
	app.Version = version
	app.GitCommit = gitCommit
	app.BuildTime = buildTime
	if err := app.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
