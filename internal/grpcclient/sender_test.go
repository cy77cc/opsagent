package grpcclient

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

func TestSenderBatchToProto(t *testing.T) {
	m1 := collector.NewMetric(
		"cpu_usage",
		map[string]string{"host": "web-1"},
		map[string]interface{}{"value": float64(85.3)},
		collector.Gauge,
		time.UnixMilli(1700000000000),
	)
	m2 := collector.NewMetric(
		"request_count",
		map[string]string{"host": "web-1"},
		map[string]interface{}{"value": int64(42)},
		collector.Counter,
		time.UnixMilli(1700000001000),
	)

	batch := metricsToBatch([]*collector.Metric{m1, m2})

	if len(batch.Metrics) != 2 {
		t.Fatalf("expected 2 metrics in batch, got %d", len(batch.Metrics))
	}
	if batch.Metrics[0].Name != "cpu_usage" {
		t.Errorf("expected name cpu_usage, got %s", batch.Metrics[0].Name)
	}
	if batch.Metrics[1].Name != "request_count" {
		t.Errorf("expected name request_count, got %s", batch.Metrics[1].Name)
	}
	if batch.Metrics[0].TimestampMs != 1700000000000 {
		t.Errorf("unexpected timestamp: %d", batch.Metrics[0].TimestampMs)
	}
}

func TestSenderEmptyBatch(t *testing.T) {
	batch := metricsToBatch(nil)
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if len(batch.Metrics) != 0 {
		t.Fatalf("expected 0 metrics, got %d", len(batch.Metrics))
	}
}

func TestSenderNewHeartbeat(t *testing.T) {
	info := &pb.AgentInfo{Hostname: "test-host", Os: "linux"}
	msg := NewHeartbeat("agent-1", "running", info)

	hb := msg.GetHeartbeat()
	if hb == nil {
		t.Fatal("expected heartbeat payload")
	}
	if hb.AgentId != "agent-1" {
		t.Errorf("expected agent-1, got %s", hb.AgentId)
	}
	if hb.Status != "running" {
		t.Errorf("expected running, got %s", hb.Status)
	}
	if hb.AgentInfo.Hostname != "test-host" {
		t.Errorf("expected test-host, got %s", hb.AgentInfo.Hostname)
	}
}

func TestSenderNewMetricBatchMessage(t *testing.T) {
	m := collector.NewMetric(
		"disk_free",
		map[string]string{"mount": "/"},
		map[string]interface{}{"value": float64(50.0)},
		collector.Gauge,
		time.Now(),
	)
	msg := NewMetricBatchMessage([]*collector.Metric{m})

	batch := msg.GetMetrics()
	if batch == nil {
		t.Fatal("expected metrics payload")
	}
	if len(batch.Metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(batch.Metrics))
	}
	if batch.Metrics[0].Name != "disk_free" {
		t.Errorf("expected disk_free, got %s", batch.Metrics[0].Name)
	}
}

func TestSenderNewExecOutputMessage(t *testing.T) {
	msg := NewExecOutputMessage("task-1", "stdout", []byte("hello"))

	out := msg.GetExecOutput()
	if out == nil {
		t.Fatal("expected exec_output payload")
	}
	if out.TaskId != "task-1" {
		t.Errorf("expected task-1, got %s", out.TaskId)
	}
	if out.Stream != "stdout" {
		t.Errorf("expected stdout, got %s", out.Stream)
	}
	if string(out.Data) != "hello" {
		t.Errorf("expected hello, got %s", string(out.Data))
	}
}

func TestSenderNewExecResultMessage(t *testing.T) {
	result := &ExecResult{
		TaskID:   "task-2",
		ExitCode: 0,
		Duration: 5 * time.Second,
		TimedOut: false,
		Stats: &ExecStats{
			PeakMemoryBytes: 1024 * 1024,
			CPUTimeUserMs:   500,
			ProcessCount:    1,
		},
	}
	msg := NewExecResultMessage(result)

	pr := msg.GetExecResult()
	if pr == nil {
		t.Fatal("expected exec_result payload")
	}
	if pr.TaskId != "task-2" {
		t.Errorf("expected task-2, got %s", pr.TaskId)
	}
	if pr.DurationMs != 5000 {
		t.Errorf("expected 5000ms, got %d", pr.DurationMs)
	}
	if pr.Stats == nil {
		t.Fatal("expected stats")
	}
	if pr.Stats.PeakMemoryBytes != 1024*1024 {
		t.Errorf("expected 1MB, got %d", pr.Stats.PeakMemoryBytes)
	}
}

func TestSenderNewRegistrationMessage(t *testing.T) {
	info := &pb.AgentInfo{Hostname: "h", Os: "linux", Arch: "amd64"}
	msg := NewRegistrationMessage("agent-1", "token-abc", info, []string{"exec", "metrics"})

	reg := msg.GetRegistration()
	if reg == nil {
		t.Fatal("expected registration payload")
	}
	if reg.AgentId != "agent-1" {
		t.Errorf("expected agent-1, got %s", reg.AgentId)
	}
	if reg.Token != "token-abc" {
		t.Errorf("expected token-abc, got %s", reg.Token)
	}
	if len(reg.Capabilities) != 2 {
		t.Errorf("expected 2 caps, got %d", len(reg.Capabilities))
	}
}

func TestSenderNewAckMessage(t *testing.T) {
	msg := NewAckMessage("ref-123", true, "")

	ack := msg.GetAck()
	if ack == nil {
		t.Fatal("expected ack payload")
	}
	if ack.RefId != "ref-123" {
		t.Errorf("expected ref-123, got %s", ack.RefId)
	}
	if !ack.Success {
		t.Error("expected success=true")
	}
}

func TestExecResultToProtoNilStats(t *testing.T) {
	result := &ExecResult{
		TaskID:   "task-3",
		ExitCode: 1,
		Duration: 2 * time.Second,
		TimedOut: true,
	}
	pr := result.ToProto()
	if pr.Stats != nil {
		t.Error("expected nil stats in proto")
	}
	if !pr.TimedOut {
		t.Error("expected timed_out=true")
	}
}

func TestSenderNewConfigUpdateAck(t *testing.T) {
	msg := NewConfigUpdateAck("cfg-ref-1", true, "")

	ack := msg.GetAck()
	if ack == nil {
		t.Fatal("expected ack payload")
	}
	if ack.RefId != "cfg-ref-1" {
		t.Errorf("expected cfg-ref-1, got %s", ack.RefId)
	}
	if !ack.Success {
		t.Error("expected success=true")
	}
	if ack.Error != "" {
		t.Errorf("expected empty error, got %s", ack.Error)
	}
}

func TestSenderNewConfigUpdateAck_Failure(t *testing.T) {
	msg := NewConfigUpdateAck("cfg-ref-2", false, "invalid config")

	ack := msg.GetAck()
	if ack == nil {
		t.Fatal("expected ack payload")
	}
	if ack.RefId != "cfg-ref-2" {
		t.Errorf("expected cfg-ref-2, got %s", ack.RefId)
	}
	if ack.Success {
		t.Error("expected success=false")
	}
	if ack.Error != "invalid config" {
		t.Errorf("expected 'invalid config', got %s", ack.Error)
	}
}

func TestSenderExecResultWithAllFields(t *testing.T) {
	result := &ExecResult{
		TaskID:    "task-full",
		ExitCode:  42,
		Duration:  10 * time.Second,
		TimedOut:  true,
		Truncated: true,
		Killed:    true,
		Stats: &ExecStats{
			PeakMemoryBytes: 2048,
			CPUTimeUserMs:   100,
			CPUTimeSystemMs: 50,
			ProcessCount:    3,
			BytesWritten:    4096,
			BytesRead:       8192,
		},
	}
	msg := NewExecResultMessage(result)

	pr := msg.GetExecResult()
	if pr == nil {
		t.Fatal("expected exec_result payload")
	}
	if pr.TaskId != "task-full" {
		t.Errorf("expected task-full, got %s", pr.TaskId)
	}
	if pr.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", pr.ExitCode)
	}
	if pr.DurationMs != 10000 {
		t.Errorf("expected 10000ms, got %d", pr.DurationMs)
	}
	if !pr.TimedOut {
		t.Error("expected timed_out=true")
	}
	if !pr.Truncated {
		t.Error("expected truncated=true")
	}
	if !pr.Killed {
		t.Error("expected killed=true")
	}
	if pr.Stats == nil {
		t.Fatal("expected stats")
	}
	if pr.Stats.CpuTimeSystemMs != 50 {
		t.Errorf("expected system cpu time 50, got %d", pr.Stats.CpuTimeSystemMs)
	}
	if pr.Stats.BytesWritten != 4096 {
		t.Errorf("expected bytes written 4096, got %d", pr.Stats.BytesWritten)
	}
	if pr.Stats.BytesRead != 8192 {
		t.Errorf("expected bytes read 8192, got %d", pr.Stats.BytesRead)
	}
}

func TestExecResultToProto_TruncatedAndKilled(t *testing.T) {
	result := &ExecResult{
		TaskID:    "task-tk",
		ExitCode:  137,
		Duration:  30 * time.Second,
		TimedOut:  false,
		Truncated: true,
		Killed:    true,
		Stats: &ExecStats{
			PeakMemoryBytes: 512 * 1024,
			CPUTimeUserMs:   200,
			CPUTimeSystemMs: 100,
			ProcessCount:    2,
			BytesWritten:    1024,
			BytesRead:       2048,
		},
	}
	pr := result.ToProto()

	if pr.TaskId != "task-tk" {
		t.Errorf("expected task-tk, got %s", pr.TaskId)
	}
	if pr.ExitCode != 137 {
		t.Errorf("expected exit code 137, got %d", pr.ExitCode)
	}
	if pr.DurationMs != 30000 {
		t.Errorf("expected 30000ms, got %d", pr.DurationMs)
	}
	if pr.TimedOut {
		t.Error("expected timed_out=false")
	}
	if !pr.Truncated {
		t.Error("expected truncated=true")
	}
	if !pr.Killed {
		t.Error("expected killed=true")
	}
	if pr.Stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if pr.Stats.PeakMemoryBytes != 512*1024 {
		t.Errorf("expected peak memory %d, got %d", 512*1024, pr.Stats.PeakMemoryBytes)
	}
	if pr.Stats.ProcessCount != 2 {
		t.Errorf("expected process count 2, got %d", pr.Stats.ProcessCount)
	}
}

func TestSenderNewExecOutputMessage_EmptyData(t *testing.T) {
	msg := NewExecOutputMessage("task-empty", "stderr", nil)

	out := msg.GetExecOutput()
	if out == nil {
		t.Fatal("expected exec_output payload")
	}
	if out.TaskId != "task-empty" {
		t.Errorf("expected task-empty, got %s", out.TaskId)
	}
	if out.Stream != "stderr" {
		t.Errorf("expected stderr, got %s", out.Stream)
	}
	if out.Data != nil {
		t.Errorf("expected nil data, got %v", out.Data)
	}
}
