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

func TestOutputStreamerZeroFlushInterval(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	sender := func(data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	}

	// Zero flush interval means intervalFlush returns immediately (no goroutine).
	os := NewOutputStreamer("t4", "stdout", 1024, 0, sender)
	os.Write([]byte("data"))
	os.Flush() // Manual flush.

	mu.Lock()
	got := string(received)
	mu.Unlock()

	if got != "data" {
		t.Errorf("expected manual flush with zero interval, got %q", got)
	}
	os.Stop()
}

func TestOutputStreamerNilSender(t *testing.T) {
	// Nil sender should not panic.
	os := NewOutputStreamer("t5", "stdout", 10, 10*time.Second, nil)
	os.Write([]byte("hello"))
	os.Flush()
	os.Stop()
}

func TestOutputStreamerMultipleWrites(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	sender := func(data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	}

	os := NewOutputStreamer("t6", "stdout", 100, 10*time.Second, sender)
	defer os.Stop()

	for i := 0; i < 5; i++ {
		os.Write([]byte("abcdefghij")) // 10 bytes each
	}
	os.Flush()

	mu.Lock()
	got := string(received)
	mu.Unlock()

	expected := "abcdefghijabcdefghijabcdefghijabcdefghijabcdefghij"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
