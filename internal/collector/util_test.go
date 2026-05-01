package collector

import (
	"testing"
)

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name   string
		input  interface{}
		want   float64
		wantOK bool
	}{
		{"float64", float64(3.14), 3.14, true},
		{"float32", float32(2.5), 2.5, true},
		{"int", int(42), 42.0, true},
		{"int32", int32(10), 10.0, true},
		{"int64", int64(100), 100.0, true},
		{"uint", uint(7), 7.0, true},
		{"uint32", uint32(99), 99.0, true},
		{"uint64", uint64(256), 256.0, true},
		{"string default", "not-a-number", 0, false},
		{"bool default", true, 0, false},
		{"nil default", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToFloat64(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ToFloat64(%v) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("ToFloat64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractNumericValue_PreferredKeys(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]interface{}
		want   float64
	}{
		{
			"value key",
			map[string]interface{}{"value": 42.0, "other": 99.0},
			42.0,
		},
		{
			"count key",
			map[string]interface{}{"count": int64(10), "other": 99.0},
			10.0,
		},
		{
			"gauge key",
			map[string]interface{}{"gauge": float32(7.5), "other": 99.0},
			7.5,
		},
		{
			"value checked before count",
			map[string]interface{}{"value": 1.0, "count": 2.0},
			1.0,
		},
		{
			"count checked before gauge",
			map[string]interface{}{"count": 3.0, "gauge": 4.0},
			3.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractNumericValue(tt.fields)
			if got != tt.want {
				t.Errorf("ExtractNumericValue(%v) = %v, want %v", tt.fields, got, tt.want)
			}
		})
	}
}

func TestExtractNumericValue_FallbackToAny(t *testing.T) {
	fields := map[string]interface{}{
		"temperature": 72.5,
		"label":       "hot",
	}
	got := ExtractNumericValue(fields)
	if got != 72.5 {
		t.Errorf("ExtractNumericValue fallback = %v, want 72.5", got)
	}
}

func TestExtractNumericValue_NoNumeric(t *testing.T) {
	fields := map[string]interface{}{
		"label": "hot",
		"name":  "test",
	}
	got := ExtractNumericValue(fields)
	if got != 0 {
		t.Errorf("ExtractNumericValue non-numeric = %v, want 0", got)
	}
}

func TestExtractNumericValue_Empty(t *testing.T) {
	got := ExtractNumericValue(map[string]interface{}{})
	if got != 0 {
		t.Errorf("ExtractNumericValue empty = %v, want 0", got)
	}
}

func TestExtractNumericValue_PreferredKeyNonNumeric(t *testing.T) {
	// "value" exists but is a string, so it should skip to fallback
	fields := map[string]interface{}{
		"value": "not-a-number",
		"temp":  98.6,
	}
	got := ExtractNumericValue(fields)
	if got != 98.6 {
		t.Errorf("ExtractNumericValue = %v, want 98.6", got)
	}
}
