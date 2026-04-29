package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/grpcclient"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
	"github.com/cy77cc/opsagent/internal/logger"
	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/cy77cc/opsagent/internal/reporter"
	"github.com/cy77cc/opsagent/internal/sandbox"
	"github.com/cy77cc/opsagent/internal/server"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	// Blank imports to trigger init() plugin registration.
	_ "github.com/cy77cc/opsagent/internal/collector/aggregators/avg"
	_ "github.com/cy77cc/opsagent/internal/collector/aggregators/sum"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/cpu"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/disk"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/memory"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/net"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/process"
	_ "github.com/cy77cc/opsagent/internal/collector/outputs/http"
	_ "github.com/cy77cc/opsagent/internal/collector/outputs/prometheus"
	_ "github.com/cy77cc/opsagent/internal/collector/outputs/promrw"
	_ "github.com/cy77cc/opsagent/internal/collector/processors/regex"
	_ "github.com/cy77cc/opsagent/internal/collector/processors/tagger"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Agent wires collection, reporting, local API server, and task dispatch.
type Agent struct {
	cfg           *config.Config
	log           zerolog.Logger
	manager       *collector.Manager
	reporter      reporter.Reporter
	server        *server.Server
	executor      *executor.Executor
	pluginRuntime *pluginruntime.Runtime
	scheduler     *collector.Scheduler
	grpcClient    *grpcclient.Client
	sandboxExec   *sandbox.Executor
	startedAt     time.Time
	activeTasks   sync.Map
}

// NewRootCommand creates the CLI entrypoint.
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
			fmt.Println("opsagent", Version)
		},
	}
	rootCmd.AddCommand(versionCmd)

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

	// Build collector pipeline from config.
	sched, err := buildScheduler(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("build scheduler: %w", err)
	}

	// Build gRPC client.
	grpcCfg := grpcclient.Config{
		ServerAddr:       cfg.GRPC.ServerAddr,
		AgentID:          cfg.Agent.ID,
		EnrollmentToken:  cfg.GRPC.EnrollToken,
		CertPath:         cfg.GRPC.MTLS.CertFile,
		KeyPath:          cfg.GRPC.MTLS.KeyFile,
		CAPath:           cfg.GRPC.MTLS.CAFile,
		HeartbeatSeconds: cfg.GRPC.HeartbeatIntervalSeconds,
		ReconnectMaxSec:  cfg.GRPC.ReconnectMaxBackoffMS / 1000,
	}
	grpcRecv := grpcclient.NewReceiver(log)
	grpcCl := grpcclient.NewClient(grpcCfg, log, grpcRecv)

	// Build sandbox executor (only when enabled).
	var sandboxExec *sandbox.Executor
	if cfg.Sandbox.Enabled {
		sandboxExec = sandbox.NewExecutor(sandbox.Config{
			NsjailPath:         cfg.Sandbox.NsjailPath,
			WorkDir:            cfg.Sandbox.BaseWorkdir,
			CgroupBase:         cfg.Sandbox.CgroupBasePath,
			TimeoutSec:         cfg.Sandbox.DefaultTimeoutSeconds,
			MaxConcurrentTasks: cfg.Sandbox.MaxConcurrentTasks,
			AuditLogPath:       cfg.Sandbox.AuditLogPath,
			Policy: sandbox.Policy{
				AllowedCommands:     cfg.Sandbox.Policy.AllowedCommands,
				BlockedCommands:     cfg.Sandbox.Policy.BlockedCommands,
				BlockedKeywords:     cfg.Sandbox.Policy.BlockedKeywords,
				AllowedInterpreters: cfg.Sandbox.Policy.AllowedInterpreters,
				MaxScriptBytes:      cfg.Sandbox.Policy.ScriptMaxBytes,
			},
		}, log)
	}

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
		scheduler:     sched,
		grpcClient:    grpcCl,
		sandboxExec:   sandboxExec,
		startedAt:     startedAt,
	}
	a.registerTaskHandlers(dispatcher)
	a.registerGRPCHandlers(grpcRecv)
	return a, nil
}

// buildScheduler creates a Scheduler from the collector config, using the default registry.
func buildScheduler(cfg *config.Config, log zerolog.Logger) (*collector.Scheduler, error) {
	interval := time.Duration(cfg.Agent.IntervalSeconds) * time.Second
	var scheduledInputs []collector.ScheduledInput

	for _, inCfg := range cfg.Collector.Inputs {
		factory, ok := collector.DefaultRegistry.GetInput(inCfg.Type)
		if !ok {
			return nil, fmt.Errorf("unknown input type: %q", inCfg.Type)
		}
		input := factory()
		if err := input.Init(inCfg.Config); err != nil {
			return nil, fmt.Errorf("init input %q: %w", inCfg.Type, err)
		}
		scheduledInputs = append(scheduledInputs, collector.ScheduledInput{
			Input:    input,
			Interval: interval,
		})
	}

	if len(scheduledInputs) == 0 {
		return nil, nil
	}

	// Build processors from config.
	var processors []collector.Processor
	for _, pCfg := range cfg.Collector.Processors {
		factory, ok := collector.DefaultRegistry.GetProcessor(pCfg.Type)
		if !ok {
			return nil, fmt.Errorf("unknown processor type: %q", pCfg.Type)
		}
		p := factory()
		if err := p.Init(pCfg.Config); err != nil {
			return nil, fmt.Errorf("init processor %q: %w", pCfg.Type, err)
		}
		processors = append(processors, p)
	}

	// Build aggregators from config.
	var aggregators []collector.Aggregator
	for _, aCfg := range cfg.Collector.Aggregators {
		factory, ok := collector.DefaultRegistry.GetAggregator(aCfg.Type)
		if !ok {
			return nil, fmt.Errorf("unknown aggregator type: %q", aCfg.Type)
		}
		agg := factory()
		if err := agg.Init(aCfg.Config); err != nil {
			return nil, fmt.Errorf("init aggregator %q: %w", aCfg.Type, err)
		}
		aggregators = append(aggregators, agg)
	}

	// Build outputs from config.
	var outputs []collector.Output
	for _, oCfg := range cfg.Collector.Outputs {
		factory, ok := collector.DefaultRegistry.GetOutput(oCfg.Type)
		if !ok {
			return nil, fmt.Errorf("unknown output type: %q", oCfg.Type)
		}
		out := factory()
		if err := out.Init(oCfg.Config); err != nil {
			return nil, fmt.Errorf("init output %q: %w", oCfg.Type, err)
		}
		outputs = append(outputs, out)
	}

	return collector.NewScheduler(scheduledInputs, processors, aggregators, outputs, log), nil
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

	// Start the collector pipeline scheduler if configured.
	var pipelineCh <-chan []*collector.Metric
	if a.scheduler != nil {
		pipelineCh = a.scheduler.Start(ctx)
		defer a.scheduler.Stop()
		a.log.Info().Msg("collector pipeline scheduler started")
	}

	// Start the gRPC client.
	if err := a.grpcClient.Start(ctx); err != nil {
		return fmt.Errorf("start grpc client: %w", err)
	}
	defer a.grpcClient.Stop()

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
		case metrics, ok := <-pipelineCh:
			if !ok {
				pipelineCh = nil
				continue
			}
			a.handlePipelineMetrics(metrics)
		case <-ticker.C:
			if err := a.collectAndReport(ctx); err != nil {
				a.log.Error().Err(err).Msg("collect loop failed")
			}
		}
	}
}

// handlePipelineMetrics processes metrics from the collector pipeline.
func (a *Agent) handlePipelineMetrics(metrics []*collector.Metric) {
	if len(metrics) == 0 {
		return
	}
	// Send metrics via gRPC client.
	a.grpcClient.SendMetrics(metrics)
	a.log.Debug().Int("count", len(metrics)).Msg("pipeline metrics sent via gRPC")
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

	// Report all collected metric payloads.
	for _, mp := range metrics {
		if reportErr := a.reporter.Report(ctx, mp); reportErr != nil {
			a.log.Error().Err(reportErr).Str("collector", mp.Collector).Msg("reporter failed")
			return reportErr
		}
	}
	// Update the server with the latest payload.
	a.server.SetLatestMetric(metrics[len(metrics)-1])
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
		a.server.SetLatestMetric(metrics[len(metrics)-1])
		return metrics, err
	})

	dispatcher.Register(task.TypeExecCommand, func(ctx context.Context, t task.AgentTask) (any, error) {
		taskCtx, cancel := context.WithCancel(ctx)
		a.activeTasks.Store(t.TaskID, cancel)
		defer a.activeTasks.Delete(t.TaskID)

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

		return a.executor.Execute(taskCtx, executor.Request{
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

	// Sandbox exec task handler.
	dispatcher.Register(task.TypeSandboxExec, func(ctx context.Context, t task.AgentTask) (any, error) {
		taskCtx, cancel := context.WithCancel(ctx)
		a.activeTasks.Store(t.TaskID, cancel)
		defer a.activeTasks.Delete(t.TaskID)

		if a.sandboxExec == nil {
			return nil, fmt.Errorf("sandbox executor is not enabled")
		}

		cmdVal, _ := t.Payload["command"].(string)
		scriptVal, _ := t.Payload["script"].(string)
		interpreterVal, _ := t.Payload["interpreter"].(string)

		if cmdVal == "" && scriptVal == "" {
			return nil, fmt.Errorf("sandbox_exec requires either 'command' or 'script' in payload")
		}

		taskID := t.TaskID
		if taskID == "" {
			taskID = fmt.Sprintf("sandbox-%d", time.Now().UnixNano())
		}

		req := sandbox.ExecRequest{
			TaskID:      taskID,
			Command:     cmdVal,
			Script:      scriptVal,
			Interpreter: interpreterVal,
		}

		if rawArgs, ok := t.Payload["args"].([]any); ok {
			for _, arg := range rawArgs {
				if s, ok := arg.(string); ok {
					req.Args = append(req.Args, s)
				}
			}
		}

		if timeoutVal, ok := t.Payload["timeout_seconds"]; ok {
			switch v := timeoutVal.(type) {
			case float64:
				req.Timeout = time.Duration(v) * time.Second
			case int:
				req.Timeout = time.Duration(v) * time.Second
			}
		}

		if scriptVal != "" {
			result, err := a.sandboxExec.ExecuteScript(taskCtx, req, nil)
			if err != nil {
				return nil, fmt.Errorf("sandbox script exec: %w", err)
			}
			return result, nil
		}

		result, err := a.sandboxExec.ExecuteCommand(taskCtx, req, nil)
		if err != nil {
			return nil, fmt.Errorf("sandbox command exec: %w", err)
		}
		return result, nil
	})
}

// registerGRPCHandlers wires platform message handlers on the gRPC receiver.
func (a *Agent) registerGRPCHandlers(recv *grpcclient.Receiver) {
	// Command handler: execute via sandbox when available, otherwise via local executor.
	recv.SetCommandHandler(func(ctx context.Context, cmd *pb.ExecuteCommand) error {
		if a.sandboxExec != nil {
			result, err := a.sandboxExec.ExecuteCommand(ctx, sandbox.ExecRequest{
				TaskID:  cmd.GetTaskId(),
				Command: cmd.GetCommand(),
				Args:    cmd.GetArgs(),
			}, nil)
			if err != nil {
				a.log.Error().Err(err).Str("task_id", cmd.GetTaskId()).Msg("sandbox exec failed")
				a.grpcClient.SendExecResult(&grpcclient.ExecResult{
					TaskID:   cmd.GetTaskId(),
					ExitCode: -1,
				})
				return nil
			}
			a.grpcClient.SendExecResult(&grpcclient.ExecResult{
				TaskID:   result.TaskID,
				ExitCode: int32(result.ExitCode),
				Duration: result.Duration,
				TimedOut: result.TimedOut,
				Killed:   result.Killed,
			})
			return nil
		}
		// Fallback to local executor.
		timeoutSec := int(cmd.GetTimeoutSeconds())
		if timeoutSec <= 0 {
			timeoutSec = a.cfg.Executor.TimeoutSeconds
		}
		res, err := a.executor.Execute(ctx, executor.Request{
			Command:        cmd.GetCommand(),
			Args:           cmd.GetArgs(),
			TimeoutSeconds: timeoutSec,
		})
		if err != nil {
			a.log.Error().Err(err).Str("task_id", cmd.GetTaskId()).Msg("exec failed")
			a.grpcClient.SendExecResult(&grpcclient.ExecResult{
				TaskID:   cmd.GetTaskId(),
				ExitCode: -1,
			})
			return nil
		}
		a.grpcClient.SendExecResult(&grpcclient.ExecResult{
			TaskID:   cmd.GetTaskId(),
			ExitCode: int32(res.ExitCode),
			Duration: time.Duration(res.DurationMS) * time.Millisecond,
		})
		return nil
	})

	// Script handler: execute via sandbox when available.
	recv.SetScriptHandler(func(ctx context.Context, script *pb.ExecuteScript) error {
		if a.sandboxExec == nil {
			a.log.Warn().Str("task_id", script.GetTaskId()).Msg("sandbox disabled, cannot execute script")
			a.grpcClient.SendExecResult(&grpcclient.ExecResult{
				TaskID:   script.GetTaskId(),
				ExitCode: -1,
			})
			return nil
		}
		result, err := a.sandboxExec.ExecuteScript(ctx, sandbox.ExecRequest{
			TaskID:      script.GetTaskId(),
			Script:      script.GetScript(),
			Interpreter: script.GetInterpreter(),
		}, func(data []byte) {
			a.grpcClient.SendExecOutput(script.GetTaskId(), "stdout", data)
		})
		if err != nil {
			a.log.Error().Err(err).Str("task_id", script.GetTaskId()).Msg("sandbox script failed")
			a.grpcClient.SendExecResult(&grpcclient.ExecResult{
				TaskID:   script.GetTaskId(),
				ExitCode: -1,
			})
			return nil
		}
		a.grpcClient.SendExecResult(&grpcclient.ExecResult{
			TaskID:   result.TaskID,
			ExitCode: int32(result.ExitCode),
			Duration: result.Duration,
			TimedOut: result.TimedOut,
			Killed:   result.Killed,
		})
		return nil
	})

	// Cancel handler: cancel active task by ID.
	recv.SetCancelHandler(func(_ context.Context, job *pb.CancelJob) error {
		taskID := job.GetTaskId()
		if cancelFn, ok := a.activeTasks.Load(taskID); ok {
			cancelFn.(context.CancelFunc)()
			a.log.Info().Str("task_id", taskID).Msg("cancel job executed")
		} else {
			a.log.Warn().Str("task_id", taskID).Msg("cancel job: task not found")
		}
		return nil
	})

	// Config update handler: ack.
	recv.SetConfigUpdateHandler(func(_ context.Context, update *pb.ConfigUpdate) error {
		a.log.Info().Int64("version", update.GetVersion()).Msg("config update received (ack)")
		a.grpcClient.SendExecResult(&grpcclient.ExecResult{TaskID: "config-update"})
		return nil
	})
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
