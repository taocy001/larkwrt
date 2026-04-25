package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

type Shell struct {
	whitelist map[string]struct{}
	timeout   time.Duration
}

func New(whitelist []string) *Shell {
	wl := make(map[string]struct{}, len(whitelist))
	for _, cmd := range whitelist {
		wl[cmd] = struct{}{}
	}
	return &Shell{whitelist: wl, timeout: defaultTimeout}
}

// Run executes cmd with args safely (no shell interpolation, whitelist enforced).
// Returns trimmed stdout and stderr.
func (s *Shell) Run(cmd string, args ...string) (string, string, error) {
	if err := s.checkWhitelist(cmd); err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, cmd, args...)
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// RunUnchecked runs without whitelist restriction — only for internal, hardcoded commands.
func RunUnchecked(timeout time.Duration, cmd string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, cmd, args...)
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func (s *Shell) checkWhitelist(cmd string) error {
	base := cmd
	if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
		base = cmd[idx+1:]
	}
	if _, ok := s.whitelist[base]; !ok {
		return fmt.Errorf("command %q not in exec whitelist", base)
	}
	return nil
}
