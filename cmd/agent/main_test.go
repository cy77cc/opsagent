package main

import (
	"testing"

	"github.com/cy77cc/opsagent/internal/app"
)

func TestRun(t *testing.T) {
	origVersion := app.Version
	origCommit := app.GitCommit
	origBuild := app.BuildTime
	defer func() {
		app.Version = origVersion
		app.GitCommit = origCommit
		app.BuildTime = origBuild
	}()

	if err := run(); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	// Verify build metadata was propagated.
	if app.Version != version {
		t.Errorf("app.Version = %q, want %q", app.Version, version)
	}
	if app.GitCommit != gitCommit {
		t.Errorf("app.GitCommit = %q, want %q", app.GitCommit, gitCommit)
	}
	if app.BuildTime != buildTime {
		t.Errorf("app.BuildTime = %q, want %q", app.BuildTime, buildTime)
	}
}

func TestRootCommandCreation(t *testing.T) {
	cmd := app.NewRootCommand()

	if cmd.Use != "opsagent" {
		t.Errorf("root command Use = %q, want opsagent", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("root command Short description should not be empty")
	}

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}

	for _, expected := range []string{"version", "run", "validate", "plugins"} {
		if !subcommands[expected] {
			t.Errorf("expected subcommand %q not found", expected)
		}
	}
}

func TestRootCommandNoArgs(t *testing.T) {
	cmd := app.NewRootCommand()
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root command with no args: %v", err)
	}
}

func TestVersionSubcommand(t *testing.T) {
	origVersion := app.Version
	origCommit := app.GitCommit
	origBuild := app.BuildTime
	defer func() {
		app.Version = origVersion
		app.GitCommit = origCommit
		app.BuildTime = origBuild
	}()

	app.Version = "test-version"
	app.GitCommit = "test-commit"
	app.BuildTime = "test-time"

	cmd := app.NewRootCommand()
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version subcommand error: %v", err)
	}
}

func TestDefaultVersionVariables(t *testing.T) {
	// Verify the default ldflags values.
	if version != "dev" {
		t.Errorf("default version = %q, want dev", version)
	}
	if gitCommit != "unknown" {
		t.Errorf("default gitCommit = %q, want unknown", gitCommit)
	}
	if buildTime != "unknown" {
		t.Errorf("default buildTime = %q, want unknown", buildTime)
	}
}
