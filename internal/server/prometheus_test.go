package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

func TestHandlePrometheusMetrics_NilRegistry(t *testing.T) {
	s := &Server{
		logger:       zerolog.Nop(),
		promRegistry: nil,
		options:      Options{Prometheus: PrometheusConfig{Enabled: true}},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.handlePrometheusMetrics(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandlePrometheusMetrics_WithRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_metric", Help: "A test metric",
	})
	reg.MustRegister(gauge)
	gauge.Set(42)

	s := &Server{
		logger:       zerolog.Nop(),
		promRegistry: reg,
		options:      Options{Prometheus: PrometheusConfig{Enabled: true}},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.handlePrometheusMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "test_metric 42") {
		t.Errorf("expected test_metric 42 in output, got:\n%s", body)
	}
}
