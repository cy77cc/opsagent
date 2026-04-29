package collector

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type mockCollector struct {
	name    string
	payload *MetricPayload
	err     error
}

func (m *mockCollector) Name() string { return m.name }
func (m *mockCollector) Collect(_ context.Context) (*MetricPayload, error) {
	return m.payload, m.err
}

func TestManagerCollectAll_Success(t *testing.T) {
	c1 := &mockCollector{
		name:    "c1",
		payload: &MetricPayload{Collector: "c1", CollectedAt: time.Now()},
	}
	c2 := &mockCollector{
		name:    "c2",
		payload: &MetricPayload{Collector: "c2", CollectedAt: time.Now()},
	}

	mgr := NewManager([]Collector{c1, c2})
	payloads, err := mgr.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("CollectAll: unexpected error: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(payloads))
	}
	if payloads[0].Collector != "c1" {
		t.Errorf("payloads[0].Collector = %q, want %q", payloads[0].Collector, "c1")
	}
	if payloads[1].Collector != "c2" {
		t.Errorf("payloads[1].Collector = %q, want %q", payloads[1].Collector, "c2")
	}
}

func TestManagerCollectAll_PartialFailure(t *testing.T) {
	c1 := &mockCollector{
		name:    "c1",
		payload: &MetricPayload{Collector: "c1", CollectedAt: time.Now()},
	}
	c2 := &mockCollector{
		name: "c2",
		err:  fmt.Errorf("collect failed"),
	}

	mgr := NewManager([]Collector{c1, c2})
	payloads, err := mgr.CollectAll(context.Background())
	if err == nil {
		t.Fatal("expected error from partial failure")
	}
	if len(payloads) != 1 {
		t.Fatalf("expected 1 successful payload, got %d", len(payloads))
	}
	if payloads[0].Collector != "c1" {
		t.Errorf("payloads[0].Collector = %q, want %q", payloads[0].Collector, "c1")
	}
}

func TestManagerCollectAll_AllFail(t *testing.T) {
	c1 := &mockCollector{name: "c1", err: fmt.Errorf("fail 1")}
	c2 := &mockCollector{name: "c2", err: fmt.Errorf("fail 2")}

	mgr := NewManager([]Collector{c1, c2})
	payloads, err := mgr.CollectAll(context.Background())
	if err == nil {
		t.Fatal("expected error when all collectors fail")
	}
	if payloads != nil {
		t.Errorf("expected nil payloads on all failures, got %d", len(payloads))
	}
}

func TestManagerCollectAll_Empty(t *testing.T) {
	mgr := NewManager(nil)
	payloads, err := mgr.CollectAll(context.Background())
	// With no collectors, CollectAll returns empty slice and no error.
	if err != nil {
		t.Fatalf("unexpected error for empty collectors: %v", err)
	}
	if len(payloads) != 0 {
		t.Errorf("expected 0 payloads, got %d", len(payloads))
	}
}
