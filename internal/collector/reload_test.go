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
