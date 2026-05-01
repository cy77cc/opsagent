package collector

import (
	"testing"

	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

func TestCollectorReloader_CanReload(t *testing.T) {
	r := NewCollectorReloader(&Scheduler{}, zerolog.Nop())

	cs := &config.ChangeSet{CollectorChanged: true}
	if !r.CanReload(cs) {
		t.Error("expected CanReload = true when CollectorChanged")
	}
	cs2 := &config.ChangeSet{CollectorChanged: false}
	if r.CanReload(cs2) {
		t.Error("expected CanReload = false when CollectorChanged is false")
	}
}

func TestToReloadConfig(t *testing.T) {
	cc := config.CollectorConfig{
		Inputs: []config.PluginInstanceConfig{
			{Type: "cpu", Config: map[string]interface{}{"per_cpu": true}},
		},
		Processors: []config.PluginInstanceConfig{
			{Type: "tagger", Config: map[string]interface{}{"tag": "v"}},
		},
	}
	rc := toReloadConfig(cc)
	if len(rc.Inputs) != 1 {
		t.Errorf("expected 1 input, got %d", len(rc.Inputs))
	}
	if rc.Inputs[0].Type != "cpu" {
		t.Errorf("expected input type 'cpu', got %q", rc.Inputs[0].Type)
	}
	if len(rc.Processors) != 1 {
		t.Errorf("expected 1 processor, got %d", len(rc.Processors))
	}
}

func TestToReloadConfig_Empty(t *testing.T) {
	cc := config.CollectorConfig{}
	rc := toReloadConfig(cc)
	if len(rc.Inputs) != 0 {
		t.Errorf("expected 0 inputs, got %d", len(rc.Inputs))
	}
	if len(rc.Processors) != 0 {
		t.Errorf("expected 0 processors, got %d", len(rc.Processors))
	}
	if len(rc.Aggregators) != 0 {
		t.Errorf("expected 0 aggregators, got %d", len(rc.Aggregators))
	}
	if len(rc.Outputs) != 0 {
		t.Errorf("expected 0 outputs, got %d", len(rc.Outputs))
	}
}

func TestToReloadConfig_AllSections(t *testing.T) {
	cc := config.CollectorConfig{
		Inputs: []config.PluginInstanceConfig{
			{Type: "cpu", Config: map[string]interface{}{"per_cpu": true}},
			{Type: "mem", Config: map[string]interface{}{}},
		},
		Processors: []config.PluginInstanceConfig{
			{Type: "tagger", Config: map[string]interface{}{"tag": "v"}},
		},
		Aggregators: []config.PluginInstanceConfig{
			{Type: "avg", Config: map[string]interface{}{"interval": "60s"}},
		},
		Outputs: []config.PluginInstanceConfig{
			{Type: "stdout", Config: map[string]interface{}{}},
		},
	}
	rc := toReloadConfig(cc)

	if len(rc.Inputs) != 2 {
		t.Errorf("expected 2 inputs, got %d", len(rc.Inputs))
	}
	if rc.Inputs[1].Type != "mem" {
		t.Errorf("expected second input type 'mem', got %q", rc.Inputs[1].Type)
	}
	if len(rc.Processors) != 1 {
		t.Errorf("expected 1 processor, got %d", len(rc.Processors))
	}
	if len(rc.Aggregators) != 1 {
		t.Errorf("expected 1 aggregator, got %d", len(rc.Aggregators))
	}
	if rc.Aggregators[0].Type != "avg" {
		t.Errorf("expected aggregator type 'avg', got %q", rc.Aggregators[0].Type)
	}
	if len(rc.Outputs) != 1 {
		t.Errorf("expected 1 output, got %d", len(rc.Outputs))
	}
}

func TestToReloadConfig_PreservesConfigMaps(t *testing.T) {
	cfgMap := map[string]interface{}{"key": "value", "num": 42}
	cc := config.CollectorConfig{
		Inputs: []config.PluginInstanceConfig{
			{Type: "cpu", Config: cfgMap},
		},
	}
	rc := toReloadConfig(cc)

	if rc.Inputs[0].Config["key"] != "value" {
		t.Errorf("expected config key 'value', got %v", rc.Inputs[0].Config["key"])
	}
	if rc.Inputs[0].Config["num"] != 42 {
		t.Errorf("expected config num 42, got %v", rc.Inputs[0].Config["num"])
	}
}

func TestCollectorReloader_Apply(t *testing.T) {
	// Save and restore default registry.
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	RegisterInput("test-input", func() Input { return &mockInput{name: "test-input"} })
	RegisterOutput("test-output", func() Output { return &mockOutput{name: "test-output"} })

	sched := NewScheduler(nil, nil, nil, nil, zerolog.Nop())
	reloader := NewCollectorReloader(sched, zerolog.Nop())

	cfg := &config.Config{
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "test-input", Config: map[string]interface{}{}},
			},
			Outputs: []config.PluginInstanceConfig{
				{Type: "test-output", Config: map[string]interface{}{}},
			},
		},
	}

	err := reloader.Apply(cfg)
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
}

func TestCollectorReloader_Rollback(t *testing.T) {
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	RegisterInput("test-input", func() Input { return &mockInput{name: "test-input"} })

	sched := NewScheduler(nil, nil, nil, nil, zerolog.Nop())
	reloader := NewCollectorReloader(sched, zerolog.Nop())

	oldCfg := &config.Config{
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "test-input", Config: map[string]interface{}{"key": "val"}},
			},
		},
	}

	err := reloader.Rollback(oldCfg)
	if err != nil {
		t.Fatalf("Rollback() error: %v", err)
	}
}

func TestCollectorReloader_Apply_UnknownInput(t *testing.T) {
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	sched := NewScheduler(nil, nil, nil, nil, zerolog.Nop())
	reloader := NewCollectorReloader(sched, zerolog.Nop())

	cfg := &config.Config{
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "nonexistent", Config: map[string]interface{}{}},
			},
		},
	}

	err := reloader.Apply(cfg)
	if err == nil {
		t.Fatal("expected error for unknown input type, got nil")
	}
}

func TestCollectorReloader_Apply_UnknownProcessor(t *testing.T) {
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	sched := NewScheduler(nil, nil, nil, nil, zerolog.Nop())
	reloader := NewCollectorReloader(sched, zerolog.Nop())

	cfg := &config.Config{
		Collector: config.CollectorConfig{
			Processors: []config.PluginInstanceConfig{
				{Type: "nonexistent", Config: map[string]interface{}{}},
			},
		},
	}

	err := reloader.Apply(cfg)
	if err == nil {
		t.Fatal("expected error for unknown processor type, got nil")
	}
}

func TestCollectorReloader_Apply_UnknownAggregator(t *testing.T) {
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	sched := NewScheduler(nil, nil, nil, nil, zerolog.Nop())
	reloader := NewCollectorReloader(sched, zerolog.Nop())

	cfg := &config.Config{
		Collector: config.CollectorConfig{
			Aggregators: []config.PluginInstanceConfig{
				{Type: "nonexistent", Config: map[string]interface{}{}},
			},
		},
	}

	err := reloader.Apply(cfg)
	if err == nil {
		t.Fatal("expected error for unknown aggregator type, got nil")
	}
}

func TestCollectorReloader_Apply_UnknownOutput(t *testing.T) {
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	sched := NewScheduler(nil, nil, nil, nil, zerolog.Nop())
	reloader := NewCollectorReloader(sched, zerolog.Nop())

	cfg := &config.Config{
		Collector: config.CollectorConfig{
			Outputs: []config.PluginInstanceConfig{
				{Type: "nonexistent", Config: map[string]interface{}{}},
			},
		},
	}

	err := reloader.Apply(cfg)
	if err == nil {
		t.Fatal("expected error for unknown output type, got nil")
	}
}
