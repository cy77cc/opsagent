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
