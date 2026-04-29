package collector

// ExtractNumericValue returns the first numeric value found in fields.
// It checks common field names ("value", "count", "gauge") first,
// then falls back to the first numeric field found.
func ExtractNumericValue(fields map[string]interface{}) float64 {
	for _, key := range []string{"value", "count", "gauge"} {
		if v, ok := fields[key]; ok {
			if f, ok := ToFloat64(v); ok {
				return f
			}
		}
	}
	for _, v := range fields {
		if f, ok := ToFloat64(v); ok {
			return f
		}
	}
	return 0
}

// ToFloat64 converts a numeric interface{} to float64.
func ToFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	default:
		return 0, false
	}
}
