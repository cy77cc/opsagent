package collector

import (
	"time"

	pb "nodeagentx/internal/grpcclient/proto"
)

// MetricType represents the type of a metric.
type MetricType int

const (
	Gauge     MetricType = 0
	Counter   MetricType = 1
	Histogram MetricType = 2
)

// Metric represents a single data point with name, tags, fields, and metadata.
type Metric struct {
	name       string
	tags       map[string]string
	fields     map[string]interface{}
	timestamp  time.Time
	metricType MetricType
}

// NewMetric creates a Metric, copying tags and fields for isolation.
func NewMetric(name string, tags map[string]string, fields map[string]interface{}, mt MetricType, ts time.Time) *Metric {
	tagsCopy := make(map[string]string, len(tags))
	for k, v := range tags {
		tagsCopy[k] = v
	}

	fieldsCopy := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		fieldsCopy[k] = v
	}

	return &Metric{
		name:       name,
		tags:       tagsCopy,
		fields:     fieldsCopy,
		timestamp:  ts,
		metricType: mt,
	}
}

// Name returns the metric name.
func (m *Metric) Name() string { return m.name }

// Tags returns a copy of the metric tags.
func (m *Metric) Tags() map[string]string {
	out := make(map[string]string, len(m.tags))
	for k, v := range m.tags {
		out[k] = v
	}
	return out
}

// Fields returns a copy of the metric fields.
func (m *Metric) Fields() map[string]interface{} {
	out := make(map[string]interface{}, len(m.fields))
	for k, v := range m.fields {
		out[k] = v
	}
	return out
}

// Timestamp returns the metric timestamp.
func (m *Metric) Timestamp() time.Time { return m.timestamp }

// Type returns the metric type.
func (m *Metric) Type() MetricType { return m.metricType }

// AddTag adds a tag to the metric.
func (m *Metric) AddTag(key, value string) {
	m.tags[key] = value
}

// ToProto converts the Metric to its protobuf representation.
func (m *Metric) ToProto() *pb.Metric {
	pbType := pb.MetricType(m.metricType)

	pbFields := make([]*pb.Field, 0, len(m.fields))
	for k, v := range m.fields {
		f := &pb.Field{Key: k}
		switch val := v.(type) {
		case float64:
			f.Value = &pb.Field_DoubleValue{DoubleValue: val}
		case int64:
			f.Value = &pb.Field_IntValue{IntValue: val}
		case string:
			f.Value = &pb.Field_StringValue{StringValue: val}
		case bool:
			f.Value = &pb.Field_BoolValue{BoolValue: val}
		}
		pbFields = append(pbFields, f)
	}

	return &pb.Metric{
		Name:        m.name,
		Tags:        m.tags,
		Fields:      pbFields,
		TimestampMs: m.timestamp.UnixMilli(),
		Type:        pbType,
	}
}
