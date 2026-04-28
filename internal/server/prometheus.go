package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"nodeagentx/internal/collector"
)

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	payload, collected, startedAt := s.metricsSnapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderPrometheus(payload, collected, startedAt, time.Now().UTC())))
}

func renderPrometheus(payload *collector.MetricPayload, collected uint64, startedAt, now time.Time) string {
	var b strings.Builder
	b.WriteString("# HELP nodeagentx_agent_up Whether agent process is up.\n")
	b.WriteString("# TYPE nodeagentx_agent_up gauge\n")
	b.WriteString("nodeagentx_agent_up 1\n")

	uptime := now.Sub(startedAt).Seconds()
	if uptime < 0 {
		uptime = 0
	}
	b.WriteString("# HELP nodeagentx_agent_uptime_seconds Agent process uptime in seconds.\n")
	b.WriteString("# TYPE nodeagentx_agent_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_agent_uptime_seconds %.3f\n", uptime))

	b.WriteString("# HELP nodeagentx_metrics_collected_total Total collected snapshots.\n")
	b.WriteString("# TYPE nodeagentx_metrics_collected_total counter\n")
	b.WriteString(fmt.Sprintf("nodeagentx_metrics_collected_total %d\n", collected))

	if payload == nil {
		return b.String()
	}

	b.WriteString("# HELP nodeagentx_cpu_usage_percent CPU usage percent.\n")
	b.WriteString("# TYPE nodeagentx_cpu_usage_percent gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_cpu_usage_percent %.4f\n", payload.CPUUsagePercent))

	b.WriteString("# HELP nodeagentx_memory_usage_percent Memory usage percent.\n")
	b.WriteString("# TYPE nodeagentx_memory_usage_percent gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_memory_usage_percent %.4f\n", payload.MemoryUsagePercent))

	b.WriteString("# HELP nodeagentx_disk_usage_percent Disk usage percent.\n")
	b.WriteString("# TYPE nodeagentx_disk_usage_percent gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_disk_usage_percent %.4f\n", payload.DiskUsagePercent))

	b.WriteString("# HELP nodeagentx_load1 Host load average over 1 minute.\n")
	b.WriteString("# TYPE nodeagentx_load1 gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_load1 %.4f\n", payload.LoadAverage.Load1))

	b.WriteString("# HELP nodeagentx_load5 Host load average over 5 minutes.\n")
	b.WriteString("# TYPE nodeagentx_load5 gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_load5 %.4f\n", payload.LoadAverage.Load5))

	b.WriteString("# HELP nodeagentx_load15 Host load average over 15 minutes.\n")
	b.WriteString("# TYPE nodeagentx_load15 gauge\n")
	b.WriteString(fmt.Sprintf("nodeagentx_load15 %.4f\n", payload.LoadAverage.Load15))

	b.WriteString("# HELP nodeagentx_network_bytes_sent Total bytes sent.\n")
	b.WriteString("# TYPE nodeagentx_network_bytes_sent counter\n")
	b.WriteString(fmt.Sprintf("nodeagentx_network_bytes_sent %d\n", payload.NetworkIO.BytesSent))

	b.WriteString("# HELP nodeagentx_network_bytes_recv Total bytes received.\n")
	b.WriteString("# TYPE nodeagentx_network_bytes_recv counter\n")
	b.WriteString(fmt.Sprintf("nodeagentx_network_bytes_recv %d\n", payload.NetworkIO.BytesRecv))

	return b.String()
}
