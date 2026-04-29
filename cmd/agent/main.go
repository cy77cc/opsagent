package main

import (
	"os"

	"github.com/cy77cc/opsagent/internal/app"
)

func main() {
	if err := app.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
