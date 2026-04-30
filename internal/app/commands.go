package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/logger"
	"github.com/spf13/cobra"
)

// NewRootCommand creates the root cobra command with run, version, validate,
// and plugins subcommands.
func NewRootCommand() *cobra.Command {
	var configPath string

	rootCmd := &cobra.Command{
		Use:   "opsagent",
		Short: "Node metrics and remote exec agent",
	}

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("opsagent %s (commit: %s, built: %s)\n", Version, GitCommit, BuildTime)
		},
	}
	rootCmd.AddCommand(versionCmd)

	var dryRun bool
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run telemetry exec agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			logLevel := os.Getenv("LOG_LEVEL")
			if logLevel == "" {
				logLevel = "info"
			}
			log := logger.New(logLevel)

			agent, err := NewAgent(cfg, log)
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if dryRun {
				return agent.RunOnce(ctx)
			}

			// SIGHUP handler for config reload.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGHUP)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case sig := <-sigCh:
						if sig == syscall.SIGHUP {
							yaml, readErr := os.ReadFile(configPath)
							if readErr != nil {
								log.Error().Err(readErr).Msg("failed to read config file for SIGHUP reload")
								continue
							}
							if applyErr := agent.ConfigReloader().Apply(ctx, yaml); applyErr != nil {
								log.Error().Err(applyErr).Msg("SIGHUP config reload failed")
							} else {
								log.Info().Msg("config reloaded via SIGHUP")
							}
						}
					}
				}
			}()

			return agent.Run(ctx)
		},
	}
	runCmd.Flags().StringVar(&configPath, "config", "./configs/config.yaml", "Path to config file")
	runCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run one collection cycle and exit")

	rootCmd.AddCommand(runCmd)

	// validate subcommand
	validateCmd := newValidateCommand()
	rootCmd.AddCommand(validateCmd)

	// plugins subcommand
	pluginsCmd := newPluginsCommand()
	rootCmd.AddCommand(pluginsCmd)

	return rootCmd
}

// newValidateCommand creates the validate subcommand which loads and prints
// the resolved configuration. Exits with code 1 on failure.
func newValidateCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("config validation failed: %w", err)
			}
			fmt.Println("Config loaded successfully")
			fmt.Println("\nResolved config:")
			fmt.Printf("  agent.id: %q\n", cfg.Agent.ID)
			fmt.Printf("  agent.interval_seconds: %d\n", cfg.Agent.IntervalSeconds)
			fmt.Printf("  server.listen_addr: %q\n", cfg.Server.ListenAddr)
			fmt.Printf("  grpc.server_addr: %q\n", cfg.GRPC.ServerAddr)
			fmt.Printf("  plugin.enabled: %v\n", cfg.Plugin.Enabled)
			fmt.Printf("  sandbox.enabled: %v\n", cfg.Sandbox.Enabled)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "./configs/config.yaml", "Path to config file")
	return cmd
}

// newPluginsCommand creates the plugins subcommand which lists all registered
// built-in plugins from the collector DefaultRegistry.
func newPluginsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "List available plugins",
		RunE: func(_ *cobra.Command, _ []string) error {
			reg := collector.DefaultRegistry
			fmt.Println("Built-in plugins:")
			fmt.Printf("  INPUTS:      %s\n", strings.Join(reg.ListInputs(), ", "))
			fmt.Printf("  PROCESSORS:  %s\n", strings.Join(reg.ListProcessors(), ", "))
			fmt.Printf("  AGGREGATORS: %s\n", strings.Join(reg.ListAggregators(), ", "))
			fmt.Printf("  OUTPUTS:     %s\n", strings.Join(reg.ListOutputs(), ", "))
			return nil
		},
	}
}
