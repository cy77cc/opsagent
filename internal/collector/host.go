package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// HostCollector collects base host metrics with gopsutil.
type HostCollector struct {
	agentID        string
	agentName      string
	agentStartedAt time.Time
}

// NewHostCollector creates a host collector.
func NewHostCollector(agentID, agentName string, startedAt time.Time) *HostCollector {
	return &HostCollector{
		agentID:        agentID,
		agentName:      agentName,
		agentStartedAt: startedAt,
	}
}

// Name returns the collector name.
func (c *HostCollector) Name() string {
	return "host"
}

// Collect gathers one host metrics snapshot.
func (c *HostCollector) Collect(ctx context.Context) (*MetricPayload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("get hostname: %w", err)
	}

	hostInfo, err := host.InfoWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get host info: %w", err)
	}

	cpuPercentages, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return nil, fmt.Errorf("get cpu usage: %w", err)
	}
	cpuUsage := 0.0
	if len(cpuPercentages) > 0 {
		cpuUsage = cpuPercentages[0]
	}

	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get memory usage: %w", err)
	}

	diskPath := "/"
	if runtime.GOOS == "windows" {
		diskPath = filepath.VolumeName(os.Getenv("SystemDrive")) + "\\"
	}
	diskStat, err := disk.UsageWithContext(ctx, diskPath)
	if err != nil {
		return nil, fmt.Errorf("get disk usage: %w", err)
	}

	networkStats, err := gnet.IOCountersWithContext(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("get network io: %w", err)
	}
	ioCounter := NetworkIO{}
	if len(networkStats) > 0 {
		ioCounter.BytesSent = networkStats[0].BytesSent
		ioCounter.BytesRecv = networkStats[0].BytesRecv
	}

	loadAverage, err := load.AvgWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get load average: %w", err)
	}

	payload := &MetricPayload{
		AgentID:            c.agentID,
		AgentName:          c.agentName,
		Collector:          c.Name(),
		CollectedAt:        time.Now().UTC(),
		AgentStartedAt:     c.agentStartedAt.UTC(),
		Hostname:           hostname,
		OS:                 hostInfo.OS,
		Platform:           hostInfo.Platform,
		PlatformVersion:    hostInfo.PlatformVersion,
		KernelVersion:      hostInfo.KernelVersion,
		CPUUsagePercent:    cpuUsage,
		MemoryUsagePercent: memStat.UsedPercent,
		DiskUsagePercent:   diskStat.UsedPercent,
		NetworkIO:          ioCounter,
		LoadAverage: LoadAverage{
			Load1:  loadAverage.Load1,
			Load5:  loadAverage.Load5,
			Load15: loadAverage.Load15,
		},
	}

	return payload, nil
}
