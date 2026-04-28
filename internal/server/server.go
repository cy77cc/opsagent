package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"nodeagentx/internal/collector"
	"nodeagentx/internal/executor"
	"nodeagentx/internal/task"
)

// AuthConfig controls optional bearer auth middleware.
type AuthConfig struct {
	Enabled     bool
	BearerToken string
}

// PrometheusConfig controls exporter route behavior.
type PrometheusConfig struct {
	Enabled         bool
	Path            string
	ProtectWithAuth bool
}

// Options configures server runtime behavior.
type Options struct {
	Auth       AuthConfig
	Prometheus PrometheusConfig
}

// Server hosts local HTTP APIs for health, metrics, tasks, and command exec.
type Server struct {
	logger     zerolog.Logger
	httpServer *http.Server
	executor   *executor.Executor
	dispatcher *task.Dispatcher
	options    Options

	mu               sync.RWMutex
	latestMetric     *collector.MetricPayload
	startedAt        time.Time
	metricsCollected uint64
}

// New creates a HTTP server with routes and middleware.
func New(listenAddr string, logger zerolog.Logger, exec *executor.Executor, dispatcher *task.Dispatcher, startedAt time.Time, options Options) *Server {
	if options.Prometheus.Path == "" {
		options.Prometheus.Path = "/metrics"
	}

	s := &Server{
		logger:     logger,
		executor:   exec,
		dispatcher: dispatcher,
		startedAt:  startedAt,
		options:    options,
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Addr:              listenAddr,
		Handler:           s.withMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.logger.Info().Str("addr", s.httpServer.Addr).Msg("http server starting")
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// SetLatestMetric updates the latest metric snapshot.
func (s *Server) SetLatestMetric(metric *collector.MetricPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestMetric = metric
	s.metricsCollected++
}

// LatestMetricExists indicates whether one metric snapshot has been collected.
func (s *Server) LatestMetricExists() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestMetric != nil
}

func (s *Server) metricsSnapshot() (*collector.MetricPayload, uint64, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestMetric, s.metricsCollected, s.startedAt
}

func (s *Server) getLatestMetric() *collector.MetricPayload {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestMetric
}
