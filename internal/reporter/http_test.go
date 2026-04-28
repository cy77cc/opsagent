package reporter

import (
	"context"
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
