package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"nodeagentx/internal/collector"
	"nodeagentx/internal/config"
	"nodeagentx/internal/executor"
	"nodeagentx/internal/logger"
	"nodeagentx/internal/pluginruntime"
	"nodeagentx/internal/reporter"
	"nodeagentx/internal/server"
	"nodeagentx/internal/task"
)

// Agent wires collection, reporting, local API server, and task dispatch.
type Agent struct {
	cfg           *config.Config
	log           zerolog.Logger
	manager       *collector.Manager
	reporter      reporter.Reporter
	server        *server.Server
	executor      *executor.Executor
	pluginRuntime *pluginruntime.Runtime
	startedAt     time.Time
}

// NewRootCommand creates the CLI entrypoint.
func NewRootCommand() *cobra.Command {
	var configPath string

	rootCmd := &cobra.Command{
		Use:   "nodeagentx",
		Short: "Node metrics and remote exec agent",
	}

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

			return agent.Run(ctx)
		},
	}
	runCmd.Flags().StringVar(&configPath, "config", "./configs/config.yaml", "Path to config file")

	rootCmd.AddCommand(runCmd)
	return rootCmd
}

// NewAgent builds the runtime agent.
func NewAgent(cfg *config.Config, log zerolog.Logger) (*Agent, error) {
	startedAt := time.Now().UTC()
	hostCollector := collector.NewHostCollector(cfg.Agent.ID, cfg.Agent.Name, startedAt)
	manager := collector.NewManager([]collector.Collector{hostCollector})

	exec := executor.New(
		cfg.Executor.AllowedCommands,
		time.Duration(cfg.Executor.TimeoutSeconds)*time.Second,
		cfg.Executor.MaxOutputBytes,
	)

	rep, err := newReporter(cfg, log)
	if err != nil {
		return nil, err
	}

	pr := pluginruntime.New(pluginruntime.Config{
		Enabled:            cfg.Plugin.Enabled,
		RuntimePath:        cfg.Plugin.RuntimePath,
		SocketPath:         cfg.Plugin.SocketPath,
		AutoStart:          cfg.Plugin.AutoStart,
		StartupTimeout:     time.Duration(cfg.Plugin.StartupTimeoutSeconds) * time.Second,
		RequestTimeout:     time.Duration(cfg.Plugin.RequestTimeoutSeconds) * time.Second,
		MaxConcurrentTasks: cfg.Plugin.MaxConcurrentTasks,
		MaxResultBytes:     cfg.Plugin.MaxResultBytes,
		ChunkSizeBytes:     cfg.Plugin.ChunkSizeBytes,
		SandboxProfile:     cfg.Plugin.SandboxProfile,
	}, log)

	dispatcher := task.NewDispatcher()
	srv := server.New(
		cfg.Server.ListenAddr,
		log,
		exec,
		dispatcher,
		startedAt,
		server.Options{
			Auth: server.AuthConfig{
				Enabled:     cfg.Auth.Enabled,
				BearerToken: cfg.Auth.BearerToken,
			},
			Prometheus: server.PrometheusConfig{
				Enabled:         cfg.Prometheus.Enabled,
				Path:            cfg.Prometheus.Path,
				ProtectWithAuth: cfg.Prometheus.ProtectWithAuth,
			},
		},
	)

	a := &Agent{
		cfg:           cfg,
		log:           log,
		manager:       manager,
		reporter:      rep,
		server:        srv,
		executor:      exec,
		pluginRuntime: pr,
		startedAt:     startedAt,
	}
	a.registerTaskHandlers(dispatcher)
	return a, nil
}

func newReporter(cfg *config.Config, log zerolog.Logger) (reporter.Reporter, error) {
	switch cfg.Reporter.Mode {
	case "stdout":
		return reporter.NewStdoutReporter(log), nil
	case "http":
		return reporter.NewHTTPReporter(
			log,
			cfg.Reporter.Endpoint,
			time.Duration(cfg.Reporter.TimeoutSeconds)*time.Second,
			cfg.Reporter.RetryCount,
			time.Duration(cfg.Reporter.RetryIntervalMS)*time.Millisecond,
		), nil
	default:
		return nil, fmt.Errorf("unsupported reporter mode: %s", cfg.Reporter.Mode)
	}
}

// Run starts runtime, HTTP server and collection loop.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info().Str("agent_id", a.cfg.Agent.ID).Str("listen_addr", a.cfg.Server.ListenAddr).Msg("agent starting")

	if err := a.pluginRuntime.Start(ctx); err != nil {
		return fmt.Errorf("start plugin runtime: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.pluginRuntime.Stop(stopCtx); err != nil {
			a.log.Error().Err(err).Msg("failed to stop plugin runtime")
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.server.Start()
	}()

	if err := a.collectAndReport(ctx); err != nil {
		a.log.Error().Err(err).Msg("initial collect failed")
	}

	ticker := time.NewTicker(time.Duration(a.cfg.Agent.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := a.server.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("shutdown server: %w", err)
			}
			return nil
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("http server stopped: %w", err)
			}
			return nil
		case <-ticker.C:
			if err := a.collectAndReport(ctx); err != nil {
				a.log.Error().Err(err).Msg("collect loop failed")
			}
		}
	}
}

func (a *Agent) collectAndReport(ctx context.Context) error {
	metrics, err := a.manager.CollectAll(ctx)
	if err != nil {
		a.log.Error().Err(err).Msg("collector returned errors")
	}
	if len(metrics) == 0 {
		if err != nil {
			return err
		}
		return errors.New("collector returned no payload")
	}

	latest := metrics[0]
	a.server.SetLatestMetric(latest)
	if reportErr := a.reporter.Report(ctx, latest); reportErr != nil {
		a.log.Error().Err(reportErr).Msg("reporter failed")
		return reportErr
	}
	return err
}

func (a *Agent) registerTaskHandlers(dispatcher *task.Dispatcher) {
	dispatcher.Register(task.TypeCollectMetrics, func(ctx context.Context, _ task.AgentTask) (any, error) {
		metrics, err := a.manager.CollectAll(ctx)
		if err != nil && len(metrics) == 0 {
			return nil, err
		}
		if len(metrics) == 0 {
			return nil, errors.New("no metric payload")
		}
		a.server.SetLatestMetric(metrics[0])
		return metrics[0], err
	})

	dispatcher.Register(task.TypeExecCommand, func(ctx context.Context, t task.AgentTask) (any, error) {
		cmdVal, ok := t.Payload["command"].(string)
		if !ok || cmdVal == "" {
			return nil, fmt.Errorf("task payload.command is required")
		}

		args := make([]string, 0)
		if rawArgs, ok := t.Payload["args"].([]any); ok {
			for _, arg := range rawArgs {
				s, ok := arg.(string)
				if !ok {
					return nil, fmt.Errorf("task payload.args must be string array")
				}
				args = append(args, s)
			}
		}

		timeoutSeconds := 0
		if timeoutVal, ok := t.Payload["timeout_seconds"]; ok {
			switch v := timeoutVal.(type) {
			case float64:
				timeoutSeconds = int(v)
			case int:
				timeoutSeconds = v
			case string:
				parsed, err := strconv.Atoi(v)
				if err != nil {
					return nil, fmt.Errorf("invalid timeout_seconds: %w", err)
				}
				timeoutSeconds = parsed
			default:
				return nil, fmt.Errorf("invalid timeout_seconds type")
			}
		}

		return a.executor.Execute(ctx, executor.Request{
			Command:        cmdVal,
			Args:           args,
			TimeoutSeconds: timeoutSeconds,
		})
	})

	dispatcher.Register(task.TypeHealthCheck, func(_ context.Context, _ task.AgentTask) (any, error) {
		return map[string]any{
			"status":            "ok",
			"agent_id":          a.cfg.Agent.ID,
			"started_at":        a.startedAt,
			"has_latest_metric": a.server.LatestMetricExists(),
			"plugin_enabled":    a.cfg.Plugin.Enabled,
		}, nil
	})

	pluginTypes := []string{
		task.TypePluginLogParse,
		task.TypePluginTextProcess,
		task.TypePluginEBPFCollect,
		task.TypePluginFSScan,
		task.TypePluginConnAnalyze,
		task.TypePluginLocalProbe,
	}
	for _, tt := range pluginTypes {
		taskType := tt
		dispatcher.Register(taskType, func(ctx context.Context, t task.AgentTask) (any, error) {
			return a.executePluginTask(ctx, t, taskType)
		})
	}
}

func (a *Agent) executePluginTask(ctx context.Context, t task.AgentTask, taskType string) (any, error) {
	if !a.cfg.Plugin.Enabled {
		return nil, fmt.Errorf("plugin runtime is disabled")
	}
	taskID := t.TaskID
	if taskID == "" {
		taskID = fmt.Sprintf("plugin-%d", time.Now().UnixNano())
	}

	deadline := time.Now().Add(time.Duration(a.cfg.Plugin.RequestTimeoutSeconds) * time.Second).UnixMilli()
	res, err := a.pluginRuntime.ExecuteTask(ctx, pluginruntime.TaskRequest{
		TaskID:     taskID,
		Type:       taskType,
		DeadlineMS: deadline,
		Payload:    t.Payload,
		Chunking: pluginruntime.ChunkingConfig{
			Enabled:       true,
			MaxChunkBytes: a.cfg.Plugin.ChunkSizeBytes,
			MaxTotalBytes: a.cfg.Plugin.MaxResultBytes,
		},
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
