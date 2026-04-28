package sandbox

import (
	"sync"
	"time"
)

// OutputSender is a callback function that receives streamed output data.
type OutputSender func(data []byte)

// OutputStreamer buffers output data and flushes it via a sender callback
// either when the buffer reaches a size threshold or on a periodic interval.
type OutputStreamer struct {
	taskID        string
	stream        string
	flushSize     int
	flushInterval time.Duration
	sender        OutputSender

	mu     sync.Mutex
	buf    []byte
	stopCh chan struct{}
}

// NewOutputStreamer creates an OutputStreamer and starts its periodic flush goroutine.
func NewOutputStreamer(taskID, stream string, flushSize int, flushInterval time.Duration, sender OutputSender) *OutputStreamer {
	os := &OutputStreamer{
		taskID:        taskID,
		stream:        stream,
		flushSize:     flushSize,
		flushInterval: flushInterval,
		sender:        sender,
		stopCh:        make(chan struct{}),
	}
	go os.intervalFlush()
	return os
}

// Write appends data to the internal buffer and flushes if the buffer
// has reached the configured size threshold.
func (os *OutputStreamer) Write(data []byte) {
	os.mu.Lock()
	os.buf = append(os.buf, data...)
	shouldFlush := len(os.buf) >= os.flushSize
	os.mu.Unlock()

	if shouldFlush {
		os.Flush()
	}
}

// Flush sends all buffered data via the sender callback and clears the buffer.
func (os *OutputStreamer) Flush() {
	os.mu.Lock()
	data := os.buf
	os.buf = nil
	os.mu.Unlock()

	if len(data) > 0 && os.sender != nil {
		os.sender(data)
	}
}

// Stop closes the stop channel, stops the periodic flush goroutine,
// and flushes any remaining buffered data.
func (os *OutputStreamer) Stop() {
	close(os.stopCh)
	os.Flush()
}

// intervalFlush periodically flushes the buffer on a ticker.
func (os *OutputStreamer) intervalFlush() {
	if os.flushInterval <= 0 {
		return
	}
	ticker := time.NewTicker(os.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			os.Flush()
		case <-os.stopCh:
			return
		}
	}
}
