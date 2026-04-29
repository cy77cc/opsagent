package grpcclient

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

func TestSenderBatchToProto(t *testing.T) {
	s := &Sender{}

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

	batch := s.metricsToBatch([]*collector.Metric{m1, m2})

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
	s := &Sender{}
	batch := s.metricsToBatch(nil)
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
