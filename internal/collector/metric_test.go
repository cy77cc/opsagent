package collector

import (
	"testing"
	"time"

	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

func TestMetricNew(t *testing.T) {
	tags := map[string]string{"host": "server1", "env": "prod"}
	fields := map[string]interface{}{
		"cpu":    75.5,
		"memory": int64(1024),
		"name":   "test",
		"active": true,
	}
	ts := time.Now().Truncate(time.Millisecond)

	m := NewMetric("cpu.usage", tags, fields, Counter, ts)

	if m.Name() != "cpu.usage" {
		t.Errorf("Name() = %q, want %q", m.Name(), "cpu.usage")
	}
	if m.Type() != Counter {
		t.Errorf("Type() = %v, want %v", m.Type(), Counter)
	}
	if !m.Timestamp().Equal(ts) {
		t.Errorf("Timestamp() = %v, want %v", m.Timestamp(), ts)
	}
	if len(m.Tags()) != 2 {
		t.Errorf("Tags() len = %d, want 2", len(m.Tags()))
	}
	if m.Tags()["host"] != "server1" {
		t.Errorf("Tags[host] = %q, want %q", m.Tags()["host"], "server1")
	}
	if len(m.Fields()) != 4 {
		t.Errorf("Fields() len = %d, want 4", len(m.Fields()))
	}
	if m.Fields()["cpu"] != 75.5 {
		t.Errorf("Fields[cpu] = %v, want 75.5", m.Fields()["cpu"])
	}
}

func TestMetricAddTag(t *testing.T) {
	m := NewMetric("test", map[string]string{"a": "1"}, nil, Gauge, time.Now())
	m.AddTag("b", "2")

	tags := m.Tags()
	if len(tags) != 2 {
		t.Errorf("Tags() len = %d, want 2", len(tags))
	}
	if tags["b"] != "2" {
		t.Errorf("Tags[b] = %q, want %q", tags["b"], "2")
	}
	// Verify original map was not affected
	if tags["a"] != "1" {
		t.Errorf("Tags[a] = %q, want %q", tags["a"], "1")
	}
}

func TestMetricToProto(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewMetric("cpu.usage",
		map[string]string{"host": "server1"},
		map[string]interface{}{
			"cpu":    75.5,
			"memory": int64(1024),
			"name":   "test",
			"active": true,
		},
		Counter,
		ts,
	)

	p := m.ToProto()

	if p.GetName() != "cpu.usage" {
		t.Errorf("proto Name = %q, want %q", p.GetName(), "cpu.usage")
	}
	if p.GetTags()["host"] != "server1" {
		t.Errorf("proto Tags[host] = %q, want %q", p.GetTags()["host"], "server1")
	}
	if p.GetType() != 1 { // pb.MetricType_COUNTER
		t.Errorf("proto Type = %v, want 1 (COUNTER)", p.GetType())
	}
	if p.GetTimestampMs() != ts.UnixMilli() {
		t.Errorf("proto TimestampMs = %d, want %d", p.GetTimestampMs(), ts.UnixMilli())
	}
	if len(p.GetFields()) != 4 {
		t.Errorf("proto Fields len = %d, want 4", len(p.GetFields()))
	}

	// Verify field values by key
	fieldMap := make(map[string]interface{})
	for _, f := range p.GetFields() {
		switch v := f.GetValue().(type) {
		case *pb.Field_DoubleValue:
			fieldMap[f.GetKey()] = v.DoubleValue
		case *pb.Field_IntValue:
			fieldMap[f.GetKey()] = v.IntValue
		case *pb.Field_StringValue:
			fieldMap[f.GetKey()] = v.StringValue
		case *pb.Field_BoolValue:
			fieldMap[f.GetKey()] = v.BoolValue
		}
	}

	if fieldMap["cpu"] != 75.5 {
		t.Errorf("proto field cpu = %v, want 75.5", fieldMap["cpu"])
	}
	if fieldMap["memory"] != int64(1024) {
		t.Errorf("proto field memory = %v, want 1024", fieldMap["memory"])
	}
	if fieldMap["name"] != "test" {
		t.Errorf("proto field name = %v, want 'test'", fieldMap["name"])
	}
	if fieldMap["active"] != true {
		t.Errorf("proto field active = %v, want true", fieldMap["active"])
	}
}

func TestMetricCopyIsolation(t *testing.T) {
	origTags := map[string]string{"a": "1"}
	origFields := map[string]interface{}{"x": 1.0}

	m := NewMetric("test", origTags, origFields, Gauge, time.Now())

	// Modify original maps
	origTags["b"] = "2"
	origFields["y"] = 2.0

	// Metric should not be affected
	if len(m.Tags()) != 1 {
		t.Errorf("Tags() len = %d, want 1 after modifying original", len(m.Tags()))
	}
	if len(m.Fields()) != 1 {
		t.Errorf("Fields() len = %d, want 1 after modifying original", len(m.Fields()))
	}

	// Modify metric's maps via AddTag and direct field access
	m.AddTag("c", "3")
	if len(origTags) != 2 {
		t.Errorf("origTags len = %d, want 2 (should not be affected by AddTag)", len(origTags))
	}
}
