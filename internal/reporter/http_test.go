package reporter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type mockRoundTripper struct {
	handler func(req *http.Request) (*http.Response, error)
}

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.handler(req)
}

func TestHTTPReporterRetrySuccess(t *testing.T) {
	var attempts atomic.Int32

	r := NewHTTPReporter(zerolog.Nop(), "http://control-plane.local/report", 2*time.Second, 3, 10*time.Millisecond)
	r.client = &http.Client{
		Transport: mockRoundTripper{handler: func(req *http.Request) (*http.Response, error) {
			current := attempts.Add(1)
			if current < 3 {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("failed")),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		}},
	}

	err := r.Report(context.Background(), map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestHTTPReporterExhaustRetry(t *testing.T) {
	r := NewHTTPReporter(zerolog.Nop(), "http://control-plane.local/report", 2*time.Second, 1, 1*time.Millisecond)
	r.client = &http.Client{
		Transport: mockRoundTripper{handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
			}, nil
		}},
	}

	err := r.Report(context.Background(), map[string]any{"k": "v"})
	if err == nil {
		t.Fatalf("expected error after retry exhaustion")
	}
}

func TestHTTPReporterFirstAttemptSuccess(t *testing.T) {
	var attempts atomic.Int32
	r := NewHTTPReporter(zerolog.Nop(), "http://control-plane.local/report", 2*time.Second, 3, 10*time.Millisecond)
	r.client = &http.Client{
		Transport: mockRoundTripper{handler: func(req *http.Request) (*http.Response, error) {
			attempts.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		}},
	}

	err := r.Report(context.Background(), map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("expected success on first attempt, got error: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestHTTPReporterTransportError(t *testing.T) {
	r := NewHTTPReporter(zerolog.Nop(), "http://control-plane.local/report", 2*time.Second, 0, 0)
	r.client = &http.Client{
		Transport: mockRoundTripper{handler: func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network unreachable")
		}},
	}

	err := r.Report(context.Background(), map[string]any{"k": "v"})
	if err == nil {
		t.Fatal("expected error from transport failure")
	}
}

func TestHTTPReporterJSONMarshalError(t *testing.T) {
	r := NewHTTPReporter(zerolog.Nop(), "http://control-plane.local/report", 2*time.Second, 0, 0)

	// channels cannot be marshaled to JSON
	err := r.Report(context.Background(), make(chan int))
	if err == nil {
		t.Fatal("expected error from JSON marshal failure")
	}
}

func TestNewHTTPReporterEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		timeout       time.Duration
		retryCount    int
		retryInterval time.Duration
	}{
		{"negative timeout", -1 * time.Second, 0, 0},
		{"zero timeout", 0, 0, 0},
		{"negative retryCount", 2 * time.Second, -1, 0},
		{"negative retryInterval", 2 * time.Second, 0, -1 * time.Second},
		{"all negative", -1 * time.Second, -1, -1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewHTTPReporter(zerolog.Nop(), "http://example.com", tt.timeout, tt.retryCount, tt.retryInterval)
			if r == nil {
				t.Fatal("expected non-nil reporter")
			}
			if r.client == nil {
				t.Fatal("expected non-nil client")
			}
			if r.client.Timeout <= 0 {
				t.Errorf("expected positive timeout, got %v", r.client.Timeout)
			}
			if r.retryCount < 0 {
				t.Errorf("expected non-negative retryCount, got %d", r.retryCount)
			}
			if r.retryInterval < 0 {
				t.Errorf("expected non-negative retryInterval, got %v", r.retryInterval)
			}
		})
	}
}
