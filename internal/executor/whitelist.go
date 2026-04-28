package executor

import "strings"

// DefaultAllowedCommands contains safe command defaults for phase one.
var DefaultAllowedCommands = []string{
	"uptime",
	"df",
	"free",
	"whoami",
	"hostname",
	"ip",
	"ss",
}

func buildWhitelist(commands []string) map[string]struct{} {
	result := make(map[string]struct{}, len(commands))
	for _, cmd := range commands {
		trimmed := strings.TrimSpace(cmd)
		if trimmed == "" {
			continue
		}
		result[trimmed] = struct{}{}
	}
	return result
}
