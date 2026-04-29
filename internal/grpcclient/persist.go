package grpcclient

import (
	"encoding/json"
	"os"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// persistedMetric is a serializable representation of a metric for disk persistence.
type persistedMetric struct {
	Name      string                 `json:"name"`
	Tags      map[string]string      `json:"tags"`
	Fields    map[string]interface{} `json:"fields"`
	Type      string                 `json:"type"`
	Timestamp int64                  `json:"timestamp"`
}

// persistMetrics writes metrics to a JSON file at the given path.
func persistMetrics(metrics []*collector.Metric, path string) error {
	var persisted []persistedMetric
	for _, m := range metrics {
		pm := persistedMetric{
			Name:      m.Name(),
			Tags:      m.Tags(),
			Fields:    m.Fields(),
			Type:      metricTypeString(m.Type()),
			Timestamp: m.Timestamp().UnixMilli(),
		}
		persisted = append(persisted, pm)
	}

	data, err := json.Marshal(persisted)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// loadMetrics reads persisted metrics from a JSON file.
func loadMetrics(path string) ([]*collector.Metric, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var persisted []persistedMetric
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, err
	}

	var metrics []*collector.Metric
	for _, pm := range persisted {
		m := collector.NewMetric(
			pm.Name,
			pm.Tags,
			pm.Fields,
			metricTypeFromString(pm.Type),
			time.UnixMilli(pm.Timestamp),
		)
		metrics = append(metrics, m)
	}

	return metrics, nil
}

func metricTypeString(t collector.MetricType) string {
	switch t {
	case collector.Counter:
		return "counter"
	case collector.Gauge:
		return "gauge"
	default:
		return "gauge"
	}
}

func metricTypeFromString(s string) collector.MetricType {
	switch s {
	case "counter":
		return collector.Counter
	case "gauge":
		return collector.Gauge
	default:
		return collector.Gauge
	}
}
