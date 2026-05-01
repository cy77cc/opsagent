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
	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run sets build-time metadata and executes the root command. Extracted from
// main so it can be tested without calling os.Exit.
func run() error {
	app.Version = version
	app.GitCommit = gitCommit
	app.BuildTime = buildTime
	return app.NewRootCommand().Execute()
}
