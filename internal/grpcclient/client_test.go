package grpcclient

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestClientDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HeartbeatSeconds != 30 {
		t.Errorf("expected HeartbeatSeconds=30, got %d", cfg.HeartbeatSeconds)
	}
	if cfg.ReconnectMaxSec != 60 {
		t.Errorf("expected ReconnectMaxSec=60, got %d", cfg.ReconnectMaxSec)
	}
	if cfg.CacheMaxSize != 10000 {
		t.Errorf("expected CacheMaxSize=10000, got %d", cfg.CacheMaxSize)
	}
	if cfg.FlushIntervalSec != 10 {
		t.Errorf("expected FlushIntervalSec=10, got %d", cfg.FlushIntervalSec)
	}
}

func TestClientConfig(t *testing.T) {
	cfg := Config{
		ServerAddr:       "localhost:50051",
		AgentID:          "agent-test",
		EnrollmentToken:  "tok",
		HeartbeatSeconds: 15,
		ReconnectMaxSec:  120,
		CacheMaxSize:     500,
		FlushIntervalSec: 5,
		Capabilities:     []string{"exec", "metrics"},
	}

	logger := zerolog.Nop()
	receiver := NewReceiver(logger)
	c := NewClient(cfg, logger, receiver)

	if c.cfg.HeartbeatSeconds != 15 {
		t.Errorf("expected 15, got %d", c.cfg.HeartbeatSeconds)
	}
	if c.cfg.CacheMaxSize != 500 {
		t.Errorf("expected 500, got %d", c.cfg.CacheMaxSize)
	}
	if c.cfg.AgentID != "agent-test" {
		t.Errorf("expected agent-test, got %s", c.cfg.AgentID)
	}
}

func TestClientNilLogger(t *testing.T) {
	cfg := DefaultConfig()
	// zerolog.Logger zero value should work as a no-op logger.
	c := NewClient(cfg, zerolog.Logger{}, nil)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.cache == nil {
		t.Fatal("expected non-nil cache")
	}
}

func TestClientNotConnectedByDefault(t *testing.T) {
	cfg := DefaultConfig()
	c := NewClient(cfg, zerolog.Nop(), nil)
	if c.IsConnected() {
		t.Error("expected not connected")
	}
}

func TestClientDefaultConfigNormalization(t *testing.T) {
	cfg := Config{} // all zeros
	c := NewClient(cfg, zerolog.Nop(), nil)
	if c.cfg.HeartbeatSeconds != 30 {
		t.Errorf("expected 30, got %d", c.cfg.HeartbeatSeconds)
	}
	if c.cfg.ReconnectMaxSec != 60 {
		t.Errorf("expected 60, got %d", c.cfg.ReconnectMaxSec)
	}
	if c.cfg.CacheMaxSize != 10000 {
		t.Errorf("expected 10000, got %d", c.cfg.CacheMaxSize)
	}
	if c.cfg.FlushIntervalSec != 10 {
		t.Errorf("expected 10, got %d", c.cfg.FlushIntervalSec)
	}
}

func TestBuildAgentInfo(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	info := c.buildAgentInfo()

	if info.Hostname == "" {
		t.Error("expected non-empty hostname")
	}
	if info.Os == "" {
		t.Error("expected non-empty OS")
	}
	if info.Arch == "" {
		t.Error("expected non-empty arch")
	}
	if info.CpuCores <= 0 {
		t.Errorf("expected positive CPU cores, got %d", info.CpuCores)
	}
	if info.MemoryBytes <= 0 {
		t.Errorf("expected positive memory bytes, got %d", info.MemoryBytes)
	}
}

func TestBuildTLSCredentials_NoConfig(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	creds, err := c.buildTLSCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	// Should be insecure when no TLS config is set.
	if creds.Info().SecurityProtocol != "insecure" {
		t.Errorf("expected insecure protocol, got %s", creds.Info().SecurityProtocol)
	}
}

func TestBuildTLSCredentials_InvalidCAPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CAPath = "/nonexistent/ca.pem"
	c := NewClient(cfg, zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Error("expected error for nonexistent CA file")
	}
}

func TestBuildTLSCredentials_InvalidCertPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CertPath = "/nonexistent/cert.pem"
	cfg.KeyPath = "/nonexistent/key.pem"
	c := NewClient(cfg, zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Error("expected error for nonexistent cert files")
	}
}
