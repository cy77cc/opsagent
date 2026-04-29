package grpcclient

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	pb "github.com/cy77cc/nodeagentx/internal/grpcclient/proto"
)

// CommandHandler handles ExecuteCommand platform messages.
type CommandHandler func(ctx context.Context, cmd *pb.ExecuteCommand) error

// ScriptHandler handles ExecuteScript platform messages.
type ScriptHandler func(ctx context.Context, script *pb.ExecuteScript) error

// CancelHandler handles CancelJob platform messages.
type CancelHandler func(ctx context.Context, job *pb.CancelJob) error

// ConfigUpdateHandler handles ConfigUpdate platform messages.
type ConfigUpdateHandler func(ctx context.Context, update *pb.ConfigUpdate) error

// Receiver dispatches incoming PlatformMessages to registered handlers.
type Receiver struct {
	logger   zerolog.Logger
	onCmd    CommandHandler
	onScript ScriptHandler
	onCancel CancelHandler
	onConfig ConfigUpdateHandler
}

// NewReceiver creates a Receiver with the given logger.
func NewReceiver(logger zerolog.Logger) *Receiver {
	return &Receiver{logger: logger}
}

// SetCommandHandler registers the handler for ExecuteCommand messages.
func (r *Receiver) SetCommandHandler(h CommandHandler) { r.onCmd = h }

// SetScriptHandler registers the handler for ExecuteScript messages.
func (r *Receiver) SetScriptHandler(h ScriptHandler) { r.onScript = h }

// SetCancelHandler registers the handler for CancelJob messages.
func (r *Receiver) SetCancelHandler(h CancelHandler) { r.onCancel = h }

// SetConfigUpdateHandler registers the handler for ConfigUpdate messages.
func (r *Receiver) SetConfigUpdateHandler(h ConfigUpdateHandler) { r.onConfig = h }

// Handle dispatches a PlatformMessage to the appropriate handler.
func (r *Receiver) Handle(ctx context.Context, msg *pb.PlatformMessage) error {
	if msg == nil {
		return fmt.Errorf("nil platform message")
	}

	switch p := msg.Payload.(type) {
	case *pb.PlatformMessage_ExecCommand:
		r.logger.Info().Str("task_id", p.ExecCommand.GetTaskId()).Msg("received ExecuteCommand")
		if r.onCmd != nil {
			return r.onCmd(ctx, p.ExecCommand)
		}
		r.logger.Warn().Str("task_id", p.ExecCommand.GetTaskId()).Msg("no command handler registered")

	case *pb.PlatformMessage_ExecScript:
		r.logger.Info().Str("task_id", p.ExecScript.GetTaskId()).Msg("received ExecuteScript")
		if r.onScript != nil {
			return r.onScript(ctx, p.ExecScript)
		}
		r.logger.Warn().Str("task_id", p.ExecScript.GetTaskId()).Msg("no script handler registered")

	case *pb.PlatformMessage_CancelJob:
		r.logger.Info().Str("task_id", p.CancelJob.GetTaskId()).Msg("received CancelJob")
		if r.onCancel != nil {
			return r.onCancel(ctx, p.CancelJob)
		}
		r.logger.Warn().Str("task_id", p.CancelJob.GetTaskId()).Msg("no cancel handler registered")

	case *pb.PlatformMessage_ConfigUpdate:
		r.logger.Info().Int64("version", p.ConfigUpdate.GetVersion()).Msg("received ConfigUpdate")
		if r.onConfig != nil {
			return r.onConfig(ctx, p.ConfigUpdate)
		}
		r.logger.Warn().Msg("no config update handler registered")

	case *pb.PlatformMessage_Ack:
		r.logger.Info().
			Str("ref_id", p.Ack.GetRefId()).
			Bool("success", p.Ack.GetSuccess()).
			Str("error", p.Ack.GetError()).
			Msg("received Ack")

	default:
		return fmt.Errorf("unknown platform message payload type: %T", msg.Payload)
	}

	return nil
}
