package collector

import (
	"context"
	"testing"
	"time"
)

func TestHostCollectorName(t *testing.T) {
	c := NewHostCollector("agent-1", "test-agent", time.Now())
	if got := c.Name(); got != "host" {
		t.Errorf("Name() = %q, want %q", got, "host")
	}
}

func TestHostCollectorCollect(t *testing.T) {
	startedAt := time.Now()
	c := NewHostCollector("agent-1", "test-agent", startedAt)

	payload, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}

	// Verify basic fields are populated.
	if payload.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", payload.AgentID, "agent-1")
	}
	if payload.AgentName != "test-agent" {
		t.Errorf("AgentName = %q, want %q", payload.AgentName, "test-agent")
	}
	if payload.Collector != "host" {
		t.Errorf("Collector = %q, want %q", payload.Collector, "host")
	}
	if payload.Hostname == "" {
		t.Error("expected non-empty Hostname")
	}
	if payload.OS == "" {
		t.Error("expected non-empty OS")
	}
}

func TestHostCollectorCollect_CanceledContext(t *testing.T) {
	c := NewHostCollector("agent-1", "test-agent", time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.Collect(ctx)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}
