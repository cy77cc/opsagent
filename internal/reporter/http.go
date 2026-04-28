package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// HTTPReporter reports payloads to control plane over HTTP.
type HTTPReporter struct {
	logger        zerolog.Logger
	client        *http.Client
	endpoint      string
	retryCount    int
	retryInterval time.Duration
}

// NewHTTPReporter creates a HTTP reporter with retry policy.
func NewHTTPReporter(logger zerolog.Logger, endpoint string, timeout time.Duration, retryCount int, retryInterval time.Duration) *HTTPReporter {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if retryCount < 0 {
		retryCount = 0
	}
	if retryInterval < 0 {
		retryInterval = 0
	}
	return &HTTPReporter{
		logger:        logger,
		client:        &http.Client{Timeout: timeout},
		endpoint:      endpoint,
		retryCount:    retryCount,
		retryInterval: retryInterval,
	}
}

// Report sends the payload with bounded retries.
func (r *HTTPReporter) Report(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	var lastErr error
	attempts := r.retryCount + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http report attempt %d failed: %w", attempt, err)
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				r.logger.Info().Int("attempt", attempt).Str("endpoint", r.endpoint).Msg("metrics reported via http")
				return nil
			}
			lastErr = fmt.Errorf("http report attempt %d failed with status %d", attempt, resp.StatusCode)
		}

		if attempt < attempts && r.retryInterval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.retryInterval):
			}
		}
	}

	return lastErr
}
