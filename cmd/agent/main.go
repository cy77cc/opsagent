package main

import (
	"os"

	"nodeagentx/internal/app"
)

func main() {
	if err := app.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
