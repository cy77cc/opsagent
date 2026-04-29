package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
)

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

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{"status": "ok"}})
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	latest := s.getLatestMetric()
	if latest == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Success: false, Error: "collector not ready"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{"status": "ready"}})
}

func (s *Server) handleLatestMetrics(w http.ResponseWriter, _ *http.Request) {
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

	var req executor.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	res, err := s.executor.Execute(r.Context(), req)
	if err != nil {
		s.logger.Error().Err(err).Msg("exec request failed")
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: res})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	var req task.AgentTask
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	timeoutSeconds := 15
	if timeoutVal, ok := req.Payload["timeout_seconds"]; ok {
		if seconds, ok := parseTimeoutSeconds(timeoutVal); ok && seconds > 0 {
			timeoutSeconds = seconds
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	res, err := s.dispatcher.Dispatch(ctx, req)
	if err != nil {
		s.logger.Error().Err(err).Str("task_type", req.Type).Msg("task dispatch failed")
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: err.Error()})
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
