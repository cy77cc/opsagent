package sandbox

import "testing"

func TestSanitizeTaskID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid simple", "task-123", "task-123", false},
		{"valid with dots", "task-1.2.3", "task-1.2.3", false},
		{"empty", "", "", true},
		{"path traversal dotdot", "../../etc/passwd", "", true},
		{"path traversal slash", "etc/passwd", "", true},
		{"path traversal backslash", `etc\passwd`, "", true},
		{"just dotdot", "..", "", true},
		{"dot only", ".", "", true},
		{"embedded dotdot", "task-../evil", "", true},
		{"null byte", "task\x00evil", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeTaskID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeTaskID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("sanitizeTaskID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
