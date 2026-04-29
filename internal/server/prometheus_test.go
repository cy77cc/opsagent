package server

import (
	"strings"
	"testing"
	"time"

	"github.com/cy77cc/nodeagentx/internal/collector"
)

func TestRenderPrometheus(t *testing.T) {
	payload := &collector.MetricPayload{
		CPUUsagePercent:    11.2,
		MemoryUsagePercent: 44.1,
		DiskUsagePercent:   22.3,
		NetworkIO: collector.NetworkIO{
			BytesSent: 123,
			BytesRecv: 456,
		},
		LoadAverage: collector.LoadAverage{Load1: 1.1, Load5: 2.2, Load15: 3.3},
	}
	out := renderPrometheus(payload, 7, time.Now().Add(-10*time.Second), time.Now())
	if !strings.Contains(out, "nodeagentx_agent_up 1") {
		t.Fatalf("missing agent_up metric")
	}
	if !strings.Contains(out, "nodeagentx_metrics_collected_total 7") {
		t.Fatalf("missing collected counter")
	}
	if !strings.Contains(out, "nodeagentx_cpu_usage_percent 11.2000") {
		t.Fatalf("missing cpu metric")
	}
}
