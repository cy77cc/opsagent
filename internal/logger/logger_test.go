package logger

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestNew_LogLevelParsing(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantLevel zerolog.Level
	}{
		{"debug", "debug", zerolog.DebugLevel},
		{"info", "info", zerolog.InfoLevel},
		{"warn", "warn", zerolog.WarnLevel},
		{"error", "error", zerolog.ErrorLevel},
		{"uppercase", "DEBUG", zerolog.DebugLevel},
		{"invalid defaults to info", "invalid", zerolog.InfoLevel},
		{"empty returns no level", "", zerolog.NoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = New(tt.level)
			if got := zerolog.GlobalLevel(); got != tt.wantLevel {
				t.Errorf("GlobalLevel() = %v, want %v", got, tt.wantLevel)
			}
		})
	}
}

func TestNew_ReturnsFunctionalLogger(t *testing.T) {
	l := New("info")
	// Should not panic.
	l.Info().Msg("test message")
	l.Debug().Msg("debug message")
	l.Error().Msg("error message")
}
