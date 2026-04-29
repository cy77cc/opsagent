package grpcclient

import (
	"context"
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
