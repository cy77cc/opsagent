package grpcclient

import (
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

// ExecStats holds resource-usage statistics for an executed task.
type ExecStats struct {
	PeakMemoryBytes int64
	CPUTimeUserMs   int64
	CPUTimeSystemMs int64
	ProcessCount    int32
	BytesWritten    int64
	BytesRead       int64
}

// ExecResult holds the outcome of a task execution.
type ExecResult struct {
	TaskID    string
	ExitCode  int32
	Duration  time.Duration
	TimedOut  bool
	Truncated bool
	Killed    bool
	Stats     *ExecStats
}

// ToProto converts an ExecResult to its protobuf representation.
func (r *ExecResult) ToProto() *pb.ExecResult {
	pr := &pb.ExecResult{
		TaskId:     r.TaskID,
		ExitCode:   r.ExitCode,
		DurationMs: r.Duration.Milliseconds(),
		TimedOut:   r.TimedOut,
		Truncated:  r.Truncated,
		Killed:     r.Killed,
	}
	if r.Stats != nil {
		pr.Stats = &pb.ExecStats{
			PeakMemoryBytes: r.Stats.PeakMemoryBytes,
			CpuTimeUserMs:   r.Stats.CPUTimeUserMs,
			CpuTimeSystemMs: r.Stats.CPUTimeSystemMs,
			ProcessCount:    r.Stats.ProcessCount,
			BytesWritten:    r.Stats.BytesWritten,
			BytesRead:       r.Stats.BytesRead,
		}
	}
	return pr
}

// metricsToBatch converts a slice of collector metrics to a MetricBatch proto.
func metricsToBatch(metrics []*collector.Metric) *pb.MetricBatch {
	pbMetrics := make([]*pb.Metric, 0, len(metrics))
	for _, m := range metrics {
		pbMetrics = append(pbMetrics, m.ToProto())
	}
	return &pb.MetricBatch{Metrics: pbMetrics}
}

// NewHeartbeat creates a heartbeat AgentMessage.
func NewHeartbeat(agentID, status string, info *pb.AgentInfo) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				AgentId:     agentID,
				TimestampMs: time.Now().UnixMilli(),
				Status:      status,
				AgentInfo:   info,
			},
		},
	}
}

// NewMetricBatchMessage wraps metrics into an AgentMessage.
func NewMetricBatchMessage(metrics []*collector.Metric) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Metrics{
			Metrics: metricsToBatch(metrics),
		},
	}
}

// NewExecOutputMessage creates an exec-output AgentMessage.
func NewExecOutputMessage(taskID, stream string, data []byte) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_ExecOutput{
			ExecOutput: &pb.ExecOutput{
				TaskId:      taskID,
				Stream:      stream,
				Data:        data,
				TimestampMs: time.Now().UnixMilli(),
			},
		},
	}
}

// NewExecResultMessage creates an exec-result AgentMessage.
func NewExecResultMessage(result *ExecResult) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_ExecResult{
			ExecResult: result.ToProto(),
		},
	}
}

// NewRegistrationMessage creates a registration AgentMessage.
func NewRegistrationMessage(agentID, token string, info *pb.AgentInfo, caps []string) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Registration{
			Registration: &pb.AgentRegistration{
				AgentId:      agentID,
				Token:        token,
				AgentInfo:    info,
				Capabilities: caps,
			},
		},
	}
}

// NewAckMessage creates an ack AgentMessage for a given reference ID.
func NewAckMessage(refID string, success bool, errMsg string) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Ack{
			Ack: &pb.Ack{
				RefId:   refID,
				Success: success,
				Error:   errMsg,
			},
		},
	}
}

// NewConfigUpdateAck creates a config-update ack message (convenience).
func NewConfigUpdateAck(refID string, success bool, errMsg string) *pb.AgentMessage {
	return NewAckMessage(refID, success, errMsg)
}
