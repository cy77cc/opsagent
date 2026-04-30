package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditLogger_Log(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	al, err := NewAuditLogger(path, 10, 3)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer al.Close()

	al.Log(AuditEvent{
		EventType: "task.completed",
		Component: "dispatcher",
		Action:    "exec_command",
		Status:    "success",
		Details:   map[string]interface{}{"task_id": "t-1"},
	})

	al.Log(AuditEvent{
		EventType: "grpc.connected",
		Component: "grpcclient",
		Action:    "connect",
		Status:    "success",
	})

	al.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()

	var events []AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType != "task.completed" {
		t.Errorf("event[0].EventType = %q, want task.completed", events[0].EventType)
	}
	if events[0].Details["task_id"] != "t-1" {
		t.Errorf("event[0].Details[task_id] = %v, want t-1", events[0].Details["task_id"])
	}
	if events[1].EventType != "grpc.connected" {
		t.Errorf("event[1].EventType = %q, want grpc.connected", events[1].EventType)
	}
	if events[0].Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestAuditLogger_Disabled(t *testing.T) {
	var al *AuditLogger
	al.Log(AuditEvent{EventType: "test"}) // should not panic
}
