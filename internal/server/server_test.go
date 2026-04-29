package server

import (
	"context"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

func TestServerStartAndShutdown(t *testing.T) {
	log := zerolog.Nop()
	srv := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Start should return nil after graceful shutdown.
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned error after shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}

func TestServerSetLatestMetric(t *testing.T) {
	log := zerolog.Nop()
	srv := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{})

	if srv.LatestMetricExists() {
		t.Error("expected no metric initially")
	}

	srv.SetLatestMetric(&collector.MetricPayload{Collector: "test"})

	if !srv.LatestMetricExists() {
		t.Error("expected metric to exist after set")
	}
}
