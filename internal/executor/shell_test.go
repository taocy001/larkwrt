package executor

import (
	"strings"
	"testing"
	"time"
)

func TestShell_allowedCommand(t *testing.T) {
	s := New([]string{"echo"})
	stdout, _, err := s.Run("echo", "hello")
	if err != nil {
		t.Fatalf("echo should succeed: %v", err)
	}
	if stdout != "hello" {
		t.Errorf("stdout: got %q want hello", stdout)
	}
}

func TestShell_blockedCommand(t *testing.T) {
	s := New([]string{"echo"})
	_, _, err := s.Run("rm", "-rf", "/")
	if err == nil {
		t.Error("rm should be blocked (not in whitelist)")
	}
	if !strings.Contains(err.Error(), "not in exec whitelist") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestShell_emptyWhitelist(t *testing.T) {
	s := New(nil)
	_, _, err := s.Run("echo", "test")
	if err == nil {
		t.Error("empty whitelist should block all commands")
	}
}

func TestShell_noInjection(t *testing.T) {
	// Even if someone passes shell metacharacters, exec.Command prevents injection
	s := New([]string{"echo"})
	stdout, _, err := s.Run("echo", "hello; rm -rf /")
	if err != nil {
		t.Fatalf("echo with suspicious arg: %v", err)
	}
	// The argument is treated as a literal string, not shell-expanded
	if !strings.Contains(stdout, "hello; rm -rf /") {
		t.Errorf("argument should be literal, got: %q", stdout)
	}
}

func TestShell_absolutePathStripped(t *testing.T) {
	// /bin/echo should match "echo" in whitelist
	s := New([]string{"echo"})
	_, _, err := s.Run("/bin/echo", "hi")
	if err != nil {
		t.Fatalf("/bin/echo with echo whitelist should succeed: %v", err)
	}
}

func TestShell_pathTraversalBlocked(t *testing.T) {
	s := New([]string{"echo"})
	_, _, err := s.Run("../echo", "hi")
	if err == nil {
		t.Error("path traversal should be blocked")
	}
}

func TestRunUnchecked_ok(t *testing.T) {
	stdout, _, err := RunUnchecked(5*time.Second, "echo", "unchecked")
	if err != nil {
		t.Fatalf("RunUnchecked: %v", err)
	}
	if stdout != "unchecked" {
		t.Errorf("stdout: got %q want unchecked", stdout)
	}
}

func TestRunUnchecked_timeout(t *testing.T) {
	_, _, err := RunUnchecked(50*time.Millisecond, "sleep", "10")
	if err == nil {
		t.Error("long sleep should timeout")
	}
}

func TestRunUnchecked_nonexistentCommand(t *testing.T) {
	_, _, err := RunUnchecked(5*time.Second, "this-command-does-not-exist-xyz")
	if err == nil {
		t.Error("nonexistent command should error")
	}
}
