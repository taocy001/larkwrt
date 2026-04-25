//go:build !production

// testing_helpers.go exposes internals for integration tests.
// This file is excluded from production builds via the "production" build tag.

package commands

import "time"

// ── Rate limiter hooks ────────────────────────────────────────────────────────

func (r *Router) GetLimiter() *limiter          { return r.limiter }
func (r *Router) SetLimiter(l *limiter)         { r.limiter = l }
func NewTestLimiter(limit int, window time.Duration) *limiter {
	return newLimiter(limit, window)
}

// ── Router hooks ──────────────────────────────────────────────────────────────

// SetHandler registers or overrides a command handler — for test injection.
func (r *Router) SetHandler(cmd string, h Handler) {
	r.handlers[cmd] = h
}

// InitiateConfirmForTest exposes the private initiateConfirm for integration tests.
func (r *Router) InitiateConfirmForTest(ctx Context, label string, fn func() (string, error)) {
	r.initiateConfirm(ctx, label, fn)
}

// ExpireAllPendingForTest marks all pending actions as expired.
func (r *Router) ExpireAllPendingForTest() {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	for _, action := range r.pending {
		action.ExpiresAt = time.Now().Add(-time.Second)
	}
}

// ExecuteFirstPendingForTest calls executeConfirm on the first pending token.
func (r *Router) ExecuteFirstPendingForTest(ctx Context) {
	r.pendingMu.Lock()
	var token string
	for t := range r.pending {
		token = t
		break
	}
	r.pendingMu.Unlock()
	if token != "" {
		r.executeConfirm(ctx, token)
	}
}
