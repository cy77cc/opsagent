package logger

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
)

// New builds a structured zerolog logger.
func New(level string) zerolog.Logger {
	parsedLevel, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil {
		parsedLevel = zerolog.InfoLevel
	}

	zerolog.SetGlobalLevel(parsedLevel)
	l := zerolog.New(os.Stdout).With().Timestamp().Logger()
	return l
}
