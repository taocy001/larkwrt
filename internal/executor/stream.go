package executor

import (
	"bufio"
	"context"
	"os"
	"os/exec"
)

// StreamLines runs name with args, merging stdout+stderr, and sends each line
// on the returned channel. The channel is closed when the process exits.
// The error channel receives the exit error (nil on success) and is then closed.
func StreamLines(ctx context.Context, name string, args ...string) (<-chan string, <-chan error) {
	lines := make(chan string, 64)
	errc := make(chan error, 1)

	go func() {
		pr, pw, err := os.Pipe()
		if err != nil {
			errc <- err
			close(errc)
			close(lines)
			return
		}

		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdout = pw
		cmd.Stderr = pw

		if err := cmd.Start(); err != nil {
			pw.Close()
			pr.Close()
			errc <- err
			close(errc)
			close(lines)
			return
		}
		pw.Close() // parent does not write; close so reader gets EOF when child exits

		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
			}
		}
		pr.Close()

		// Send exit error before closing lines so the consumer can read it
		// synchronously after lines is drained.
		errc <- cmd.Wait()
		close(errc)
		close(lines)
	}()

	return lines, errc
}

// Stream is the whitelist-checked version of StreamLines for use by Shell.
func (s *Shell) Stream(ctx context.Context, name string, args ...string) (<-chan string, <-chan error, error) {
	if err := s.checkWhitelist(name); err != nil {
		return nil, nil, err
	}
	lines, errc := StreamLines(ctx, name, args...)
	return lines, errc, nil
}
