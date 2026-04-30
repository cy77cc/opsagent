package app

import (
	"testing"
)

func TestNewMetricsRegistry(t *testing.T) {
	reg := NewMetricsRegistry()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	mfs, err := reg.Registry().Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	names := make(map[string]bool)
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}

	expected := []string{
		"opsagent_uptime_seconds",
		"opsagent_grpc_connected",
		"opsagent_tasks_running",
		"opsagent_tasks_completed_total",
		"opsagent_tasks_failed_total",
		"opsagent_metrics_collected_total",
		"opsagent_pipeline_errors_total",
		"opsagent_plugin_requests_total",
		"opsagent_grpc_reconnects_total",
		"opsagent_cpu_usage_percent",
		"opsagent_memory_usage_percent",
		"opsagent_disk_usage_percent",
		"opsagent_load1",
		"opsagent_load5",
		"opsagent_load15",
		"opsagent_network_bytes_sent_total",
		"opsagent_network_bytes_recv_total",
	}

	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected metric %q to be registered", name)
		}
	}
}

func TestMetricsUpdateSystemGauges(t *testing.T) {
	reg := NewMetricsRegistry()
	reg.UpdateSystemMetrics(45.5, 72.3, 80.1, 1.5, 2.0, 3.0)

	mfs, err := reg.Registry().Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	for _, mf := range mfs {
		switch mf.GetName() {
		case "opsagent_cpu_usage_percent":
			val := mf.GetMetric()[0].GetGauge().GetValue()
			if val != 45.5 {
				t.Errorf("cpu_usage = %f, want 45.5", val)
			}
		case "opsagent_memory_usage_percent":
			val := mf.GetMetric()[0].GetGauge().GetValue()
			if val != 72.3 {
				t.Errorf("memory_usage = %f, want 72.3", val)
			}
		}
	}
}

func TestMetricsCounters(t *testing.T) {
	reg := NewMetricsRegistry()
	reg.IncTasksCompleted()
	reg.IncTasksCompleted()
	reg.IncTasksFailed("exec_command", "timeout")
	reg.IncMetricsCollected()
	reg.IncGRPCReconnects()

	mfs, err := reg.Registry().Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	for _, mf := range mfs {
		switch mf.GetName() {
		case "opsagent_tasks_completed_total":
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 2 {
				t.Errorf("tasks_completed = %f, want 2", val)
			}
		case "opsagent_metrics_collected_total":
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 1 {
				t.Errorf("metrics_collected = %f, want 1", val)
			}
		case "opsagent_grpc_reconnects_total":
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 1 {
				t.Errorf("grpc_reconnects = %f, want 1", val)
			}
		}
	}
}
