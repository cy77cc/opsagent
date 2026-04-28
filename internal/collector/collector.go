package collector

import (
	"context"
	"time"
)

// Collector represents a metric source unit.
type Collector interface {
	Name() string
	Collect(ctx context.Context) (*MetricPayload, error)
}

// NetworkIO captures bytes sent/received at collection time.
type NetworkIO struct {
	BytesSent uint64 `json:"bytes_sent"`
	BytesRecv uint64 `json:"bytes_recv"`
}

// LoadAverage captures host load averages.
type LoadAverage struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// MetricPayload is the unified host metric payload for phase one.
type MetricPayload struct {
	AgentID            string      `json:"agent_id"`
	AgentName          string      `json:"agent_name"`
	Collector          string      `json:"collector"`
	CollectedAt        time.Time   `json:"collected_at"`
	AgentStartedAt     time.Time   `json:"agent_started_at"`
	Hostname           string      `json:"hostname"`
	OS                 string      `json:"os"`
	Platform           string      `json:"platform"`
	PlatformVersion    string      `json:"platform_version"`
	KernelVersion      string      `json:"kernel_version"`
	CPUUsagePercent    float64     `json:"cpu_usage_percent"`
	MemoryUsagePercent float64     `json:"memory_usage_percent"`
	DiskUsagePercent   float64     `json:"disk_usage_percent"`
	NetworkIO          NetworkIO   `json:"network_io"`
	LoadAverage        LoadAverage `json:"load_average"`
}
