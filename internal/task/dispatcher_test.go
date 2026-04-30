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

func TestDispatcher_Unregister(t *testing.T) {
	d := NewDispatcher()
	d.Register("test-type", func(_ context.Context, _ AgentTask) (any, error) {
		return "ok", nil
	})

	// Verify it works before unregister.
	result, err := d.Dispatch(context.Background(), AgentTask{Type: "test-type"})
	if err != nil {
		t.Fatalf("dispatch before unregister: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %v, want ok", result)
	}

	// Unregister and verify it fails.
	d.Unregister("test-type")
	_, err = d.Dispatch(context.Background(), AgentTask{Type: "test-type"})
	if err == nil {
		t.Fatal("expected error after unregister")
	}
}

func TestDispatcher_Unregister_NonExistent(t *testing.T) {
	d := NewDispatcher()
	// Should not panic.
	d.Unregister("non-existent")
}
