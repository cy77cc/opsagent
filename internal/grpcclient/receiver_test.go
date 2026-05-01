package grpcclient

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
	"github.com/rs/zerolog"
)

func TestReceiverHandle_DispatchesToCorrectHandler(t *testing.T) {
	tests := []struct {
		name      string
		msg       *pb.PlatformMessage
		wantCmd   bool
		wantScrip bool
		wantCanc  bool
		wantConf  bool
	}{
		{
			name: "command message dispatches to onCmd",
			msg: &pb.PlatformMessage{
				Payload: &pb.PlatformMessage_ExecCommand{
					ExecCommand: &pb.ExecuteCommand{TaskId: "cmd-1", Command: "ls"},
				},
			},
			wantCmd: true,
		},
		{
			name: "script message dispatches to onScript",
			msg: &pb.PlatformMessage{
				Payload: &pb.PlatformMessage_ExecScript{
					ExecScript: &pb.ExecuteScript{TaskId: "scr-1", Script: "echo hi"},
				},
			},
			wantScrip: true,
		},
		{
			name: "cancel message dispatches to onCancel",
			msg: &pb.PlatformMessage{
				Payload: &pb.PlatformMessage_CancelJob{
					CancelJob: &pb.CancelJob{TaskId: "cancel-1", Reason: "timeout"},
				},
			},
			wantCanc: true,
		},
		{
			name: "config update message dispatches to onConfig",
			msg: &pb.PlatformMessage{
				Payload: &pb.PlatformMessage_ConfigUpdate{
					ConfigUpdate: &pb.ConfigUpdate{Version: 42},
				},
			},
			wantConf: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var gotCmd, gotScript, gotCancel, gotConfig bool

			r := NewReceiver(zerolog.Nop())
			r.SetCommandHandler(func(_ context.Context, cmd *pb.ExecuteCommand) error {
				mu.Lock()
				defer mu.Unlock()
				gotCmd = true
				return nil
			})
			r.SetScriptHandler(func(_ context.Context, script *pb.ExecuteScript) error {
				mu.Lock()
				defer mu.Unlock()
				gotScript = true
				return nil
			})
			r.SetCancelHandler(func(_ context.Context, job *pb.CancelJob) error {
				mu.Lock()
				defer mu.Unlock()
				gotCancel = true
				return nil
			})
			r.SetConfigUpdateHandler(func(_ context.Context, update *pb.ConfigUpdate) error {
				mu.Lock()
				defer mu.Unlock()
				gotConfig = true
				return nil
			})

			err := r.Handle(context.Background(), tt.msg)
			if err != nil {
				t.Fatalf("Handle returned unexpected error: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if gotCmd != tt.wantCmd {
				t.Errorf("onCmd called = %v, want %v", gotCmd, tt.wantCmd)
			}
			if gotScript != tt.wantScrip {
				t.Errorf("onScript called = %v, want %v", gotScript, tt.wantScrip)
			}
			if gotCancel != tt.wantCanc {
				t.Errorf("onCancel called = %v, want %v", gotCancel, tt.wantCanc)
			}
			if gotConfig != tt.wantConf {
				t.Errorf("onConfig called = %v, want %v", gotConfig, tt.wantConf)
			}
		})
	}
}

func TestReceiverHandle_NilHandler(t *testing.T) {
	// Receiver with no handlers registered should not panic.
	r := NewReceiver(zerolog.Nop())

	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{TaskId: "cmd-nil", Command: "whoami"},
		},
	}

	err := r.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle returned unexpected error with nil handler: %v", err)
	}
}

func TestReceiverHandle_NilPayload(t *testing.T) {
	// A PlatformMessage with no payload set should return an error, not panic.
	r := NewReceiver(zerolog.Nop())
	r.SetCommandHandler(func(_ context.Context, _ *pb.ExecuteCommand) error {
		return nil
	})

	msg := &pb.PlatformMessage{} // Payload is nil

	err := r.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("Handle should return an error for nil payload")
	}
	t.Logf("got expected error: %v", err)
}

func TestReceiverHandle_NilMessage(t *testing.T) {
	// A nil PlatformMessage should return an error, not panic.
	r := NewReceiver(zerolog.Nop())

	err := r.Handle(context.Background(), nil)
	if err == nil {
		t.Fatal("Handle should return an error for nil message")
	}
	t.Logf("got expected error: %v", err)
}

func TestReceiverHandle_AckBranch(t *testing.T) {
	r := NewReceiver(zerolog.Nop())

	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_Ack{
			Ack: &pb.Ack{
				RefId:   "ref-123",
				Success: true,
				Error:   "",
			},
		},
	}

	err := r.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle ack returned unexpected error: %v", err)
	}
}

func TestReceiverHandle_HandlerErrorPropagation(t *testing.T) {
	r := NewReceiver(zerolog.Nop())

	expectedErr := fmt.Errorf("command handler failed")
	r.SetCommandHandler(func(_ context.Context, _ *pb.ExecuteCommand) error {
		return expectedErr
	})

	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{TaskId: "cmd-err", Command: "fail"},
		},
	}

	err := r.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error from handler")
	}
	if err.Error() != expectedErr.Error() {
		t.Errorf("expected %q, got %q", expectedErr.Error(), err.Error())
	}
}

func TestReceiverHandle_ScriptHandlerError(t *testing.T) {
	r := NewReceiver(zerolog.Nop())

	expectedErr := fmt.Errorf("script handler failed")
	r.SetScriptHandler(func(_ context.Context, _ *pb.ExecuteScript) error {
		return expectedErr
	})

	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecScript{
			ExecScript: &pb.ExecuteScript{TaskId: "scr-err", Script: "bad"},
		},
	}

	err := r.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error from script handler")
	}
	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestReceiverHandle_CancelHandlerError(t *testing.T) {
	r := NewReceiver(zerolog.Nop())

	expectedErr := fmt.Errorf("cancel handler failed")
	r.SetCancelHandler(func(_ context.Context, _ *pb.CancelJob) error {
		return expectedErr
	})

	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_CancelJob{
			CancelJob: &pb.CancelJob{TaskId: "cancel-err", Reason: "test"},
		},
	}

	err := r.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error from cancel handler")
	}
	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestReceiverHandle_ConfigUpdateHandlerError(t *testing.T) {
	r := NewReceiver(zerolog.Nop())

	expectedErr := fmt.Errorf("config handler failed")
	r.SetConfigUpdateHandler(func(_ context.Context, _ *pb.ConfigUpdate) error {
		return expectedErr
	})

	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ConfigUpdate{
			ConfigUpdate: &pb.ConfigUpdate{Version: 1},
		},
	}

	err := r.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error from config handler")
	}
	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestReceiverHandle_NilHandlerForScript(t *testing.T) {
	r := NewReceiver(zerolog.Nop())
	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecScript{
			ExecScript: &pb.ExecuteScript{TaskId: "scr-nil", Script: "echo hi"},
		},
	}
	err := r.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle returned unexpected error with nil script handler: %v", err)
	}
}

func TestReceiverHandle_NilHandlerForCancel(t *testing.T) {
	r := NewReceiver(zerolog.Nop())
	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_CancelJob{
			CancelJob: &pb.CancelJob{TaskId: "cancel-nil", Reason: "test"},
		},
	}
	err := r.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle returned unexpected error with nil cancel handler: %v", err)
	}
}

func TestReceiverHandle_NilHandlerForConfigUpdate(t *testing.T) {
	r := NewReceiver(zerolog.Nop())
	msg := &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ConfigUpdate{
			ConfigUpdate: &pb.ConfigUpdate{Version: 1},
		},
	}
	err := r.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle returned unexpected error with nil config handler: %v", err)
	}
}
