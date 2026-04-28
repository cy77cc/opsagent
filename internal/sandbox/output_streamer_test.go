package sandbox

import (
	"sync"
	"testing"
	"time"
)

func TestOutputStreamerFlushBySize(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	sender := func(data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	}

	os := NewOutputStreamer("t1", "stdout", 10, 10*time.Second, sender)
	defer os.Stop()

	os.Write([]byte("12345"))      // 5 bytes, below threshold
	os.Write([]byte("67890abcd"))  // 9 more = 14 total, triggers flush

	// Give a tiny moment for the flush to propagate.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	got := string(received)
	mu.Unlock()

	if got != "1234567890abcd" {
		t.Errorf("expected flush by size, got %q", got)
	}
}

func TestOutputStreamerFlushByInterval(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	sender := func(data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	}

	os := NewOutputStreamer("t2", "stdout", 1024, 50*time.Millisecond, sender)
	defer os.Stop()

	os.Write([]byte("hello"))

	// Wait for the interval flush to fire.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := string(received)
	mu.Unlock()

	if got != "hello" {
		t.Errorf("expected interval flush, got %q", got)
	}
}

func TestOutputStreamerFlushRemaining(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	sender := func(data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	}

	os := NewOutputStreamer("t3", "stdout", 1024, 10*time.Second, sender)
	os.Write([]byte("partial"))
	os.Stop() // Should flush remaining

	mu.Lock()
	got := string(received)
	mu.Unlock()

	if got != "partial" {
		t.Errorf("expected Stop to flush remaining, got %q", got)
	}
}
