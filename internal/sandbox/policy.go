package sandbox

import (
	"fmt"
	"strings"
)

// Policy defines the security rules for sandbox command and script execution.
type Policy struct {
	AllowedCommands    []string `json:"allowed_commands"`
	BlockedCommands    []string `json:"blocked_commands"`
	BlockedKeywords    []string `json:"blocked_keywords"`
	AllowedInterpreters []string `json:"allowed_interpreters"`
	MaxScriptBytes     int      `json:"max_script_bytes"`
	AllowSudo          bool     `json:"allow_sudo"`
	AllowNetwork       bool     `json:"allow_network"`
}

// ValidateCommand checks whether a command with its arguments is allowed under this policy.
// It checks blocked list first, then allowed list (if non-empty), then shell metacharacters, then sudo.
func (p *Policy) ValidateCommand(command string, args []string) error {
	cmdName := strings.TrimSpace(command)
	if cmdName == "" {
		return fmt.Errorf("command is required")
	}

	// Check blocked commands first — they always take precedence.
	blocked := toSet(p.BlockedCommands)
	if _, ok := blocked[cmdName]; ok {
		return fmt.Errorf("command %q is blocked by policy", cmdName)
	}

	// If an allowed list is specified, the command must be on it.
	if len(p.AllowedCommands) > 0 {
		allowed := toSet(p.AllowedCommands)
		if _, ok := allowed[cmdName]; !ok {
			return fmt.Errorf("command %q is not in the allowed list", cmdName)
		}
	}

	// Check arguments for shell metacharacters.
	for i, arg := range args {
		if containsShellMetacharacters(arg) {
			return fmt.Errorf("argument %d contains shell metacharacters: %q", i, arg)
		}
	}

	// Block sudo unless explicitly allowed.
	if cmdName == "sudo" && !p.AllowSudo {
		return fmt.Errorf("sudo is not allowed by policy")
	}

	return nil
}

// ValidateScript checks whether a script executed by the given interpreter is allowed.
func (p *Policy) ValidateScript(interpreter, script string) error {
	if interpreter == "" {
		return fmt.Errorf("interpreter is required")
	}

	// Check interpreter whitelist.
	if len(p.AllowedInterpreters) > 0 {
		allowed := toSet(p.AllowedInterpreters)
		if _, ok := allowed[interpreter]; !ok {
			return fmt.Errorf("interpreter %q is not allowed", interpreter)
		}
	}

	// Check script size.
	maxBytes := p.MaxScriptBytes
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024 // 1 MB default
	}
	if len(script) > maxBytes {
		return fmt.Errorf("script size %d bytes exceeds maximum %d bytes", len(script), maxBytes)
	}

	// Check blocked keywords (case-insensitive).
	lowerScript := strings.ToLower(script)
	for _, kw := range p.BlockedKeywords {
		if strings.Contains(lowerScript, strings.ToLower(kw)) {
			return fmt.Errorf("script contains blocked keyword %q", kw)
		}
	}

	return nil
}

// toSet converts a string slice to a map set for O(1) lookups.
func toSet(items []string) map[string]struct{} {
	result := make(map[string]struct{}, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result[trimmed] = struct{}{}
		}
	}
	return result
}

// shellMetachars lists the dangerous shell metacharacters/sequences.
var shellMetachars = []string{
	";",
	"&&",
	"||",
	"|",
	"`",
	"$(",
	"${",
	">",
	"<",
	"\n",
	"'",
	"\"",
	"\\",
	"*",
	"?",
	"#",
	"~",
}

// containsShellMetacharacters checks whether s contains any shell metacharacter.
func containsShellMetacharacters(s string) bool {
	for _, mc := range shellMetachars {
		if strings.Contains(s, mc) {
			return true
		}
	}
	return false
}
