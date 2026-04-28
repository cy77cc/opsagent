package task

import (
	"context"
	"testing"
)

func TestDispatchUnsupportedType(t *testing.T) {
	d := NewDispatcher()
	_, err := d.Dispatch(context.Background(), AgentTask{Type: "unknown"})
	if err == nil {
		t.Fatalf("expected unsupported task error")
	}
}

func TestDispatchSuccess(t *testing.T) {
	d := NewDispatcher()
	d.Register(TypeHealthCheck, func(ctx context.Context, task AgentTask) (any, error) {
		return "ok", nil
	})
	v, err := d.Dispatch(context.Background(), AgentTask{Type: TypeHealthCheck})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v.(string) != "ok" {
		t.Fatalf("unexpected result: %v", v)
	}
}
