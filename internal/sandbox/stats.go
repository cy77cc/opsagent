package sandbox

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Stats holds resource usage statistics read from a cgroup.
type Stats struct {
	PeakMemoryBytes int64 `json:"peak_memory_bytes"`
	CPUTimeUserMs   int64 `json:"cpu_time_user_ms"`
	CPUTimeSystemMs int64 `json:"cpu_time_system_ms"`
	ProcessCount    int32 `json:"process_count"`
	BytesWritten    int64 `json:"bytes_written"`
	BytesRead       int64 `json:"bytes_read"`
}

// ReadCgroupStats reads resource usage stats from the specified cgroup path.
func ReadCgroupStats(cgroupPath string) (*Stats, error) {
	stats := &Stats{}

	// Read peak memory: memory.peak (cgroup v2).
	if val, err := readSingleInt(filepath.Join(cgroupPath, "memory.peak")); err == nil {
		stats.PeakMemoryBytes = val
	}

	// Read cpu.stat for user_usec and system_usec (cgroup v2).
	cpuStatPath := filepath.Join(cgroupPath, "cpu.stat")
	if f, err := os.Open(cpuStatPath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			parts := strings.Fields(scanner.Text())
			if len(parts) != 2 {
				continue
			}
			val, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				continue
			}
			switch parts[0] {
			case "user_usec":
				stats.CPUTimeUserMs = val / 1000 // usec -> ms
			case "system_usec":
				stats.CPUTimeSystemMs = val / 1000
			}
		}
	}

	// Read pids.current.
	if val, err := readSingleInt(filepath.Join(cgroupPath, "pids.current")); err == nil {
		stats.ProcessCount = int32(val)
	}

	// Read io.stat for rbytes and wbytes.
	ioStatPath := filepath.Join(cgroupPath, "io.stat")
	if f, err := os.Open(ioStatPath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			for i := 1; i < len(fields)-1; i++ {
				switch fields[i] {
				case "rbytes":
					if v, err := strconv.ParseInt(fields[i+1], 10, 64); err == nil {
						stats.BytesRead += v
					}
				case "wbytes":
					if v, err := strconv.ParseInt(fields[i+1], 10, 64); err == nil {
						stats.BytesWritten += v
					}
				}
			}
		}
	}

	return stats, nil
}

// CreateCgroup creates a new cgroup under the base path for the given task.
func CreateCgroup(basePath, taskID string) (string, error) {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	cgroupPath := filepath.Join(basePath, fmt.Sprintf("sandbox-%s", taskID))
	if err := os.MkdirAll(cgroupPath, 0o755); err != nil {
		return "", fmt.Errorf("create cgroup %s: %w", cgroupPath, err)
	}
	return cgroupPath, nil
}

// SetCgroupLimits writes resource limits to the cgroup.
func SetCgroupLimits(cgroupPath string, memoryMB, cpuPercent, maxPIDs int) error {
	if memoryMB > 0 {
		memBytes := memoryMB * 1024 * 1024
		if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(strconv.Itoa(memBytes)), 0o644); err != nil {
			return fmt.Errorf("set memory.max: %w", err)
		}
	}

	if cpuPercent > 0 {
		// cgroup v2 cpu.max format: "$MAX $PERIOD" (100000 usec period).
		period := 100000
		max := cpuPercent * period / 100
		val := fmt.Sprintf("%d %d", max, period)
		if err := os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(val), 0o644); err != nil {
			return fmt.Errorf("set cpu.max: %w", err)
		}
	}

	if maxPIDs > 0 {
		if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(strconv.Itoa(maxPIDs)), 0o644); err != nil {
			return fmt.Errorf("set pids.max: %w", err)
		}
	}

	return nil
}

// KillCgroupProcesses writes "1" to cgroup.kill to terminate all processes in the cgroup.
func KillCgroupProcesses(cgroupPath string) error {
	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.kill"), []byte("1"), 0o644); err != nil {
		return fmt.Errorf("kill cgroup processes: %w", err)
	}
	return nil
}

// RemoveCgroup removes the cgroup directory.
func RemoveCgroup(cgroupPath string) error {
	if err := os.RemoveAll(cgroupPath); err != nil {
		return fmt.Errorf("remove cgroup %s: %w", cgroupPath, err)
	}
	return nil
}

// readSingleInt reads a single integer value from a file.
func readSingleInt(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}
