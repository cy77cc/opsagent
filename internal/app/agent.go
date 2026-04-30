package app

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/grpcclient"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/cy77cc/opsagent/internal/reporter"
	"github.com/cy77cc/opsagent/internal/sandbox"
	"github.com/cy77cc/opsagent/internal/server"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"

	// Blank imports to trigger init() plugin registration.
	_ "github.com/cy77cc/opsagent/internal/collector/aggregators/avg"
	_ "github.com/cy77cc/opsagent/internal/collector/aggregators/minmax"
	_ "github.com/cy77cc/opsagent/internal/collector/aggregators/percentile"
	_ "github.com/cy77cc/opsagent/internal/collector/aggregators/sum"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/cpu"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/disk"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/memory"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/net"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/process"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/load"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/diskio"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/temp"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/gpu"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/connections"
	_ "github.com/cy77cc/opsagent/internal/collector/outputs/http"
	_ "github.com/cy77cc/opsagent/internal/collector/outputs/prometheus"
	_ "github.com/cy77cc/opsagent/internal/collector/outputs/promrw"
	_ "github.com/cy77cc/opsagent/internal/collector/processors/regex"
	_ "github.com/cy77cc/opsagent/internal/collector/processors/delta"
	_ "github.com/cy77cc/opsagent/internal/collector/processors/tagger"
)

// Version information set at build time via ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// Agent wires collection, local API server, and task dispatch.
type Agent struct {
	cfg              *config.Config
	log              zerolog.Logger
	server           HTTPServer
	executor         *executor.Executor
	pluginRuntime    PluginRuntime
	pluginGateway    PluginGateway
	scheduler        Scheduler
	grpcClient       GRPCClient
	sandboxExec      *sandbox.Executor
	configReloader   *config.ConfigReloader
	metricsReg       *MetricsRegistry
	auditLog         *AuditLogger
	startedAt        time.Time
	activeTasks      sync.Map
	shuttingDown     atomic.Bool
	shutdownComplete chan struct{}
}

// ConfigReloader returns the agent's config reloader.
func (a *Agent) ConfigReloader() *config.ConfigReloader {
	return a.configReloader
}

// IsShutdownComplete reports whether the agent has fully shut down.
func (a *Agent) IsShutdownComplete() bool {
	select {
	case <-a.shutdownComplete:
		return true
	default:
		return false
	}
}

// NewAgent builds the runtime agent. Options allow injecting custom
// implementations of HTTPServer, PluginRuntime, Scheduler, and GRPCClient
// (primarily for testing).
func NewAgent(cfg *config.Config, log zerolog.Logger, opts ...Option) (*Agent, error) {
	a := &Agent{
		cfg:              cfg,
		log:              log,
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}

	// Build metrics registry.
	a.metricsReg = NewMetricsRegistry()

	// Build audit logger if enabled.
	if cfg.Agent.AuditLog.Enabled {
		al, err := NewAuditLogger(
			cfg.Agent.AuditLog.Path,
			cfg.Agent.AuditLog.MaxSizeMB,
			cfg.Agent.AuditLog.MaxBackups,
		)
		if err != nil {
			return nil, fmt.Errorf("create audit logger: %w", err)
		}
		a.auditLog = al
	}

	// Build executor (always concrete).
	a.executor = executor.New(
		cfg.Executor.AllowedCommands,
		time.Duration(cfg.Executor.TimeoutSeconds)*time.Second,
		cfg.Executor.MaxOutputBytes,
	)

	// Build plugin runtime if not injected.
	if a.pluginRuntime == nil {
		a.pluginRuntime = pluginruntime.New(pluginruntime.Config{
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
	}

	// Build plugin gateway if enabled and not injected.
	if a.pluginGateway == nil && cfg.PluginGateway.Enabled {
		gw := pluginruntime.NewGateway(pluginruntime.GatewayConfig{
			PluginsDir:          cfg.PluginGateway.PluginsDir,
			StartupTimeout:      time.Duration(cfg.PluginGateway.StartupTimeoutSeconds) * time.Second,
			HealthCheckInterval: time.Duration(cfg.PluginGateway.HealthCheckIntervalSecs) * time.Second,
			MaxRestarts:         cfg.PluginGateway.MaxRestarts,
			RestartBackoff:      time.Duration(cfg.PluginGateway.RestartBackoffSeconds) * time.Second,
			FileWatchDebounce:   time.Duration(cfg.PluginGateway.FileWatchDebounceSecs) * time.Second,
			PluginConfigs:       cfg.PluginGateway.PluginConfigs,
		}, log)
		a.pluginGateway = gw
	}

	// Build collector pipeline scheduler if not injected.
	if a.scheduler == nil {
		sched, err := buildScheduler(cfg, log)
		if err != nil {
			return nil, fmt.Errorf("build scheduler: %w", err)
		}
		a.scheduler = sched
	}

	// Build gRPC client if not injected.
	var grpcRecv *grpcclient.Receiver
	if a.grpcClient == nil {
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
		grpcRecv = grpcclient.NewReceiver(log)
		a.grpcClient = grpcclient.NewClient(grpcCfg, log, grpcRecv)
	}

	// Build sandbox executor (only when enabled, always concrete).
	if cfg.Sandbox.Enabled {
		a.sandboxExec = sandbox.NewExecutor(sandbox.Config{
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

	// Build HTTP server if not injected.
	dispatcher := task.NewDispatcher()
	if a.server == nil {
		a.server = server.New(
			cfg.Server.ListenAddr,
			log,
			a.executor,
			dispatcher,
			a.startedAt,
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
				PromRegistry: a.metricsReg.Registry(),
				HealthCheckers: server.HealthCheckers{
					GRPC:      a.grpcClient,
					Scheduler: a.scheduler,
					PluginRT:  a.pluginRuntime,
				},
			},
		)
	}

	a.registerTaskHandlers(dispatcher)
	if grpcRecv != nil {
		a.registerGRPCHandlers(grpcRecv)
	}

	// Build config reloader if not injected.
	if a.configReloader == nil {
		var reloaders []config.Reloader
		if sched, ok := a.scheduler.(*collector.Scheduler); ok {
			reloaders = append(reloaders, collector.NewCollectorReloader(sched, log))
		}
		if srv, ok := a.server.(*server.Server); ok {
			reloaders = append(reloaders, server.NewAuthReloader(srv))
			reloaders = append(reloaders, server.NewPrometheusReloader(srv))
		}
		reloaders = append(reloaders, reporter.NewReporterReloader(log))
		a.configReloader = config.NewConfigReloader(cfg, log, reloaders...)
	}

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

// startSubsystems initialises and starts all agent subsystems. It returns the
// collector pipeline channel and an error channel for the HTTP server.
func (a *Agent) startSubsystems(ctx context.Context) (<-chan []*collector.Metric, chan error, error) {
	a.log.Info().Str("agent_id", a.cfg.Agent.ID).Str("listen_addr", a.cfg.Server.ListenAddr).Msg("agent starting")

	if err := a.pluginRuntime.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start plugin runtime: %w", err)
	}

	if a.pluginGateway != nil {
		if err := a.pluginGateway.Start(ctx); err != nil {
			return nil, nil, fmt.Errorf("start plugin gateway: %w", err)
		}
	}

	var pipelineCh <-chan []*collector.Metric
	if a.scheduler != nil {
		pipelineCh = a.scheduler.Start(ctx)
		a.log.Info().Msg("collector pipeline scheduler started")
	}

	if err := a.grpcClient.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start grpc client: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.server.Start()
	}()

	return pipelineCh, errCh, nil
}

// eventLoop blocks until the context is cancelled, the HTTP server exits, or
// the pipeline channel is closed.
func (a *Agent) eventLoop(ctx context.Context, pipelineCh <-chan []*collector.Metric, errCh chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if err != nil {
				a.log.Error().Err(err).Msg("http server stopped with error")
			}
			return
		case metrics, ok := <-pipelineCh:
			if !ok {
				pipelineCh = nil
				continue
			}
			a.handlePipelineMetrics(metrics)
		}
	}
}

// waitForActiveTasks blocks until all active tasks complete or the context is cancelled.
func (a *Agent) waitForActiveTasks(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.activeTasks.Range(func(key, value any) bool {
				value.(context.CancelFunc)()
				return true
			})
			return
		case <-ticker.C:
			remaining := 0
			a.activeTasks.Range(func(_, _ any) bool { remaining++; return true })
			if remaining == 0 {
				return
			}
		}
	}
}

// shutdown gracefully tears down all subsystems in the ordered steps:
// 1. Mark as shutting down (reject new tasks).
// 2. Wait for active tasks to complete.
// 3. Stop scheduler.
// 4. Flush gRPC cache.
// 5. Stop plugin runtime.
// 6. Shutdown HTTP server.
func (a *Agent) shutdown(ctx context.Context) {
	// 1. Mark as shutting down.
	a.shuttingDown.Store(true)
	a.auditLog.Log(AuditEvent{
		EventType: "agent.shutting_down", Component: "agent",
		Action: "shutdown", Status: "success",
	})

	// 2. Wait for active tasks.
	a.waitForActiveTasks(ctx)

	// 3. Stop scheduler.
	if a.scheduler != nil {
		a.scheduler.Stop()
	}

	// 4. Flush gRPC cache.
	if err := a.grpcClient.FlushAndStop(ctx, a.cfg.GRPC.CachePersistPath); err != nil {
		a.log.Error().Err(err).Msg("failed to flush gRPC client")
	}

	// 5. Stop plugin runtime.
	stopCtx, stopCancel := context.WithTimeout(ctx, 5*time.Second)
	defer stopCancel()
	if err := a.pluginRuntime.Stop(stopCtx); err != nil {
		a.log.Error().Err(err).Msg("failed to stop plugin runtime")
	}

	// 5b. Stop plugin gateway.
	if a.pluginGateway != nil {
		if err := a.pluginGateway.Stop(stopCtx); err != nil {
			a.log.Error().Err(err).Msg("failed to stop plugin gateway")
		}
	}

	// 6. Shutdown HTTP server.
	if err := a.server.Shutdown(ctx); err != nil {
		a.log.Error().Err(err).Msg("failed to shutdown server")
	}

	a.auditLog.Log(AuditEvent{
		EventType: "agent.stopped", Component: "agent",
		Action: "stop", Status: "success",
	})
	a.auditLog.Close()
	close(a.shutdownComplete)
}

// Run starts all subsystems, enters the event loop, and shuts down on exit.
func (a *Agent) Run(ctx context.Context) error {
	pipelineCh, errCh, err := a.startSubsystems(ctx)
	if err != nil {
		return err
	}
	a.auditLog.Log(AuditEvent{
		EventType: "agent.started", Component: "agent",
		Action: "start", Status: "success",
		Details: map[string]interface{}{"agent_id": a.cfg.Agent.ID},
	})
	a.eventLoop(ctx, pipelineCh, errCh)

	timeout := time.Duration(a.cfg.Agent.ShutdownTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	a.shutdown(shutdownCtx)
	return nil
}

// RunOnce starts the collector pipeline, collects one batch of metrics,
// prints a summary, and shuts down. Useful for dry-run / debugging.
func (a *Agent) RunOnce(ctx context.Context) error {
	if a.scheduler == nil {
		return fmt.Errorf("no scheduler configured")
	}
	ch := a.scheduler.Start(ctx)
	select {
	case metrics, ok := <-ch:
		if !ok {
			return fmt.Errorf("pipeline channel closed before collecting metrics")
		}
		a.handlePipelineMetrics(metrics)
		totalFields := 0
		for _, m := range metrics {
			totalFields += len(m.Fields())
		}
		fmt.Printf("Collected %d metrics from pipeline\n", len(metrics))
	case <-ctx.Done():
		return ctx.Err()
	}
	a.scheduler.Stop()
	return nil
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

func (a *Agent) registerTaskHandlers(dispatcher *task.Dispatcher) {
	dispatcher.Register(task.TypeCollectMetrics, func(_ context.Context, _ task.AgentTask) (any, error) {
		return nil, fmt.Errorf("legacy collect-metrics path removed")
	})

	dispatcher.Register(task.TypeExecCommand, func(ctx context.Context, t task.AgentTask) (any, error) {
		if a.shuttingDown.Load() {
			a.auditLog.Log(AuditEvent{
				EventType: "task.failed", Component: "dispatcher",
				Action: "exec_command", Status: "failure",
				Details: map[string]interface{}{"task_id": t.TaskID},
				Error:   "agent is shutting down",
			})
			return nil, fmt.Errorf("agent is shutting down")
		}

		a.auditLog.Log(AuditEvent{
			EventType: "task.started", Component: "dispatcher",
			Action: "exec_command", Status: "success",
			Details: map[string]interface{}{"task_id": t.TaskID},
		})
		a.metricsReg.TasksRunning.Inc()
		defer a.metricsReg.TasksRunning.Dec()

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

		res, err := a.executor.Execute(taskCtx, executor.Request{
			Command:        cmdVal,
			Args:           args,
			TimeoutSeconds: timeoutSeconds,
		})
		if err != nil {
			a.metricsReg.IncTasksFailed("exec_command", "error")
			a.auditLog.Log(AuditEvent{
				EventType: "task.failed", Component: "dispatcher",
				Action: "exec_command", Status: "failure",
				Details: map[string]interface{}{"task_id": t.TaskID},
				Error:   err.Error(),
			})
			return nil, err
		}

		a.metricsReg.IncTasksCompleted()
		a.auditLog.Log(AuditEvent{
			EventType: "task.completed", Component: "dispatcher",
			Action: "exec_command", Status: "success",
			Details: map[string]interface{}{"task_id": t.TaskID},
		})
		return res, nil
	})

	dispatcher.Register(task.TypeHealthCheck, func(_ context.Context, t task.AgentTask) (any, error) {
		a.auditLog.Log(AuditEvent{
			EventType: "task.started", Component: "dispatcher",
			Action: "health_check", Status: "success",
			Details: map[string]interface{}{"task_id": t.TaskID},
		})
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
			a.auditLog.Log(AuditEvent{
				EventType: "task.started", Component: "dispatcher",
				Action: taskType, Status: "success",
				Details: map[string]interface{}{"task_id": t.TaskID},
			})
			a.metricsReg.TasksRunning.Inc()
			defer a.metricsReg.TasksRunning.Dec()

			res, err := a.executePluginTask(ctx, t, taskType)
			if err != nil {
				a.metricsReg.IncTasksFailed(taskType, "error")
				a.auditLog.Log(AuditEvent{
					EventType: "task.failed", Component: "dispatcher",
					Action: taskType, Status: "failure",
					Details: map[string]interface{}{"task_id": t.TaskID},
					Error:   err.Error(),
				})
				return nil, err
			}

			a.metricsReg.IncTasksCompleted()
			a.auditLog.Log(AuditEvent{
				EventType: "task.completed", Component: "dispatcher",
				Action: taskType, Status: "success",
				Details: map[string]interface{}{"task_id": t.TaskID},
			})
			return res, nil
		})
	}

	// Sandbox exec task handler.
	dispatcher.Register(task.TypeSandboxExec, func(ctx context.Context, t task.AgentTask) (any, error) {
		if a.shuttingDown.Load() {
			a.auditLog.Log(AuditEvent{
				EventType: "task.failed", Component: "dispatcher",
				Action: "sandbox_exec", Status: "failure",
				Details: map[string]interface{}{"task_id": t.TaskID},
				Error:   "agent is shutting down",
			})
			return nil, fmt.Errorf("agent is shutting down")
		}

		a.auditLog.Log(AuditEvent{
			EventType: "task.started", Component: "dispatcher",
			Action: "sandbox_exec", Status: "success",
			Details: map[string]interface{}{"task_id": t.TaskID},
		})
		a.metricsReg.TasksRunning.Inc()
		defer a.metricsReg.TasksRunning.Dec()

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
				a.metricsReg.IncTasksFailed("sandbox_exec", "error")
				a.auditLog.Log(AuditEvent{
					EventType: "task.failed", Component: "dispatcher",
					Action: "sandbox_exec", Status: "failure",
					Details: map[string]interface{}{"task_id": t.TaskID},
					Error:   err.Error(),
				})
				return nil, fmt.Errorf("sandbox script exec: %w", err)
			}
			a.metricsReg.IncTasksCompleted()
			a.auditLog.Log(AuditEvent{
				EventType: "task.completed", Component: "dispatcher",
				Action: "sandbox_exec", Status: "success",
				Details: map[string]interface{}{"task_id": t.TaskID},
			})
			return result, nil
		}

		result, err := a.sandboxExec.ExecuteCommand(taskCtx, req, nil)
		if err != nil {
			a.metricsReg.IncTasksFailed("sandbox_exec", "error")
			a.auditLog.Log(AuditEvent{
				EventType: "task.failed", Component: "dispatcher",
				Action: "sandbox_exec", Status: "failure",
				Details: map[string]interface{}{"task_id": t.TaskID},
				Error:   err.Error(),
			})
			return nil, fmt.Errorf("sandbox command exec: %w", err)
		}
		a.metricsReg.IncTasksCompleted()
		a.auditLog.Log(AuditEvent{
			EventType: "task.completed", Component: "dispatcher",
			Action: "sandbox_exec", Status: "success",
			Details: map[string]interface{}{"task_id": t.TaskID},
		})
		return result, nil
	})

	// Register gateway plugin task handlers dynamically.
	if gw, ok := a.pluginGateway.(*pluginruntime.Gateway); ok {
		gw.OnPluginLoaded(func(name string, taskTypes []string) {
			for _, tt := range taskTypes {
				fullType := pluginruntime.FullTaskType(name, tt)
				ft := fullType
				dispatcher.Register(ft, func(ctx context.Context, t task.AgentTask) (any, error) {
					a.auditLog.Log(AuditEvent{
						EventType: "task.started", Component: "dispatcher",
						Action: ft, Status: "success",
						Details: map[string]interface{}{"task_id": t.TaskID},
					})
					a.metricsReg.TasksRunning.Inc()
					defer a.metricsReg.TasksRunning.Dec()

					res, err := a.executeGatewayTask(ctx, t)
					if err != nil {
						a.metricsReg.IncTasksFailed(ft, "error")
						a.auditLog.Log(AuditEvent{
							EventType: "task.failed", Component: "dispatcher",
							Action: ft, Status: "failure",
							Details: map[string]interface{}{"task_id": t.TaskID},
							Error:   err.Error(),
						})
						return nil, err
					}

					a.metricsReg.IncTasksCompleted()
					a.auditLog.Log(AuditEvent{
						EventType: "task.completed", Component: "dispatcher",
						Action: ft, Status: "success",
						Details: map[string]interface{}{"task_id": t.TaskID},
					})
					return res, nil
				})
				a.log.Info().Str("task_type", ft).Msg("registered gateway task handler")
			}
		})
		gw.OnPluginUnloaded(func(name string, taskTypes []string) {
			for _, tt := range taskTypes {
				fullType := pluginruntime.FullTaskType(name, tt)
				dispatcher.Unregister(fullType)
				a.log.Info().Str("task_type", fullType).Msg("unregistered gateway task handler")
			}
		})
	}
}

// registerGRPCHandlers wires platform message handlers on the gRPC receiver.
func (a *Agent) registerGRPCHandlers(recv *grpcclient.Receiver) {
	// Command handler: execute via sandbox when available, otherwise via local executor.
	recv.SetCommandHandler(func(ctx context.Context, cmd *pb.ExecuteCommand) error {
		if a.shuttingDown.Load() {
			return fmt.Errorf("agent is shutting down")
		}

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
		if a.shuttingDown.Load() {
			return fmt.Errorf("agent is shutting down")
		}

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

	// Config update handler: apply hot-reload via ConfigReloader.
	recv.SetConfigUpdateHandler(func(ctx context.Context, update *pb.ConfigUpdate) error {
		if err := a.configReloader.Apply(ctx, update.GetConfigYaml()); err != nil {
			a.log.Error().Err(err).Int64("version", update.GetVersion()).Msg("config reload failed")
			a.grpcClient.SendExecResult(&grpcclient.ExecResult{
				TaskID:   fmt.Sprintf("config-update-%d", update.GetVersion()),
				ExitCode: -1,
			})
			return nil
		}
		a.log.Info().Int64("version", update.GetVersion()).Msg("config reloaded")
		a.grpcClient.SendExecResult(&grpcclient.ExecResult{
			TaskID: fmt.Sprintf("config-update-%d", update.GetVersion()),
		})
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

func (a *Agent) executeGatewayTask(ctx context.Context, t task.AgentTask) (any, error) {
	if a.shuttingDown.Load() {
		return nil, fmt.Errorf("agent is shutting down")
	}
	if a.pluginGateway == nil {
		return nil, fmt.Errorf("plugin gateway is not enabled")
	}

	taskID := t.TaskID
	if taskID == "" {
		taskID = fmt.Sprintf("gw-%d", time.Now().UnixNano())
	}

	deadline := time.Now().Add(30 * time.Second).UnixMilli()
	return a.pluginGateway.ExecuteTask(ctx, pluginruntime.TaskRequest{
		TaskID:     taskID,
		Type:       t.Type,
		DeadlineMS: deadline,
		Payload:    t.Payload,
		Chunking: pluginruntime.ChunkingConfig{
			Enabled:       true,
			MaxChunkBytes: a.cfg.Plugin.ChunkSizeBytes,
			MaxTotalBytes: a.cfg.Plugin.MaxResultBytes,
		},
	})
}
