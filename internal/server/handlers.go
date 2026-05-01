package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/health"
	"github.com/cy77cc/opsagent/internal/task"
)

const maxTimeoutSeconds = 300

type apiResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/api/v1/metrics/latest", s.handleLatestMetrics)
	mux.HandleFunc("/api/v1/exec", s.handleExec)
	mux.HandleFunc("/api/v1/tasks", s.handleTask)
	if s.options.Prometheus.Enabled {
		mux.HandleFunc(s.options.Prometheus.Path, s.handlePrometheusMetrics)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	subsystems := make(map[string]any)
	overallStatus := "healthy"

	type entry struct {
		name    string
		checker health.Statuser
		isCore  bool
	}
	entries := []entry{
		{"grpc", s.healthCheckers.GRPC, true},
		{"scheduler", s.healthCheckers.Scheduler, true},
		{"plugin_runtime", s.healthCheckers.PluginRT, false},
	}

	for _, e := range entries {
		if e.checker == nil {
			subsystems[e.name] = map[string]any{"status": "unavailable"}
			if e.isCore {
				overallStatus = "unhealthy"
			} else if overallStatus == "healthy" {
				overallStatus = "degraded"
			}
			continue
		}
		st := e.checker.HealthStatus()
		subsystems[e.name] = st
		if st.Status == "error" || st.Status == "stopped" || st.Status == "disconnected" {
			if e.isCore {
				overallStatus = "unhealthy"
			} else if overallStatus == "healthy" {
				overallStatus = "degraded"
			}
		}
	}

	data := map[string]any{
		"status":     overallStatus,
		"subsystems": subsystems,
	}

	// Only expose version info when auth is enabled and request is authenticated.
	if s.options.Auth.Enabled {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		expected := "Bearer " + s.options.Auth.BearerToken
		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) == 1 {
			data["version"] = s.version
			data["git_commit"] = s.gitCommit
			data["uptime_seconds"] = int(time.Since(s.startedAt).Seconds())
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data:    data,
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	latest := s.getLatestMetric()
	if latest == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Success: false, Error: "collector not ready"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{"status": "ready"}})
}

func (s *Server) handleLatestMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	latest := s.getLatestMetric()
	if latest == nil {
		writeJSON(w, http.StatusNotFound, apiResponse{Success: false, Error: "no metrics collected yet"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: latest})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
	var req executor.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Warn().Err(err).Msg("invalid exec request body")
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: "invalid request body"})
		return
	}

	res, err := s.executor.Execute(r.Context(), req)
	if err != nil {
		s.logger.Error().Err(err).Msg("exec request failed")
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: "command execution failed"})
		return
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: res})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
	var req task.AgentTask
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Warn().Err(err).Msg("invalid task request body")
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: "invalid request body"})
		return
	}

	timeoutSeconds := 15
	if timeoutVal, ok := req.Payload["timeout_seconds"]; ok {
		if seconds, ok := parseTimeoutSeconds(timeoutVal); ok && seconds > 0 {
			timeoutSeconds = min(seconds, maxTimeoutSeconds)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	res, err := s.dispatcher.Dispatch(ctx, req)
	if err != nil {
		s.logger.Error().Err(err).Str("task_type", req.Type).Msg("task dispatch failed")
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: "task dispatch failed"})
		return
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: res})
}

func writeJSON(w http.ResponseWriter, code int, payload apiResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func parseTimeoutSeconds(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
