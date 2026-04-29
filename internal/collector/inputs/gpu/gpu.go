package gpu

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("gpu", func() collector.Input {
		return &GPUInput{}
	})
}

// GPUInput gathers NVIDIA GPU metrics via nvidia-smi.
type GPUInput struct {
	binPath   string
	available bool
	timeout   time.Duration
}

func (g *GPUInput) Init(cfg map[string]interface{}) error {
	g.timeout = 5 * time.Second

	if v, ok := cfg["bin_path"]; ok {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("gpu: bin_path must be a string, got %T", v)
		}
		g.binPath = s
	}

	// Check if nvidia-smi is available
	binPath := g.binPath
	if binPath == "" {
		binPath = "nvidia-smi"
	}
	if _, err := exec.LookPath(binPath); err != nil {
		g.available = false
		log.Info().Str("plugin", "gpu").Msg("nvidia-smi not found, skipping GPU metrics")
		return nil
	}
	g.available = true
	return nil
}

func (g *GPUInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	if !g.available {
		return nil
	}

	binPath := g.binPath
	if binPath == "" {
		binPath = "nvidia-smi"
	}

	query := "index,name,utilization.gpu,utilization.memory,memory.total,memory.used,temperature.gpu,power.draw,fan.speed"
	args := []string{
		fmt.Sprintf("--query-gpu=%s", query),
		"--format=csv,noheader,nounits",
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gpu: nvidia-smi execution failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		tags, fields, parseErr := parseGPUOutput(line)
		if parseErr != nil {
			log.Warn().Err(parseErr).Str("line", line).Msg("gpu: skipping malformed line")
			continue
		}
		acc.AddGauge("gpu", tags, fields)
	}

	return nil
}

func (g *GPUInput) SampleConfig() string {
	return `
  ## Path to nvidia-smi binary. Default is to find it in PATH.
  # bin_path = "/usr/bin/nvidia-smi"
`
}

// parseGPUOutput parses a single CSV line from nvidia-smi output.
func parseGPUOutput(line string) (map[string]string, map[string]interface{}, error) {
	parts := strings.Split(line, ", ")
	if len(parts) < 9 {
		return nil, nil, fmt.Errorf("unexpected field count: %d, want >= 9", len(parts))
	}

	tags := map[string]string{
		"gpu_index": strings.TrimSpace(parts[0]),
		"gpu_name":  strings.TrimSpace(parts[1]),
	}

	fields := map[string]interface{}{}
	floatFields := map[string]int{
		"utilization_gpu":    2,
		"utilization_memory": 3,
		"memory_total":       4,
		"memory_used":        5,
		"temperature":        6,
		"power_draw":         7,
		"fan_speed":          8,
	}

	for field, idx := range floatFields {
		v := strings.TrimSpace(parts[idx])
		if v == "[N/A]" || v == "" {
			continue
		}
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
			continue
		}
		fields[field] = f
	}

	return tags, fields, nil
}
