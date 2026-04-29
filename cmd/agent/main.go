package main

import (
	"os"

	"github.com/cy77cc/opsagent/internal/app"
)

// version is set at build time via ldflags.
var version = "dev"

func main() {
	app.Version = version
	if err := app.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
