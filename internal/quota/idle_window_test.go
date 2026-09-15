package quota

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
	"github.com/tapaixx/codex-window-reset/internal/store"
)

// idleUsageFixture reproduces what a live account with no window running
// actually returns: nothing consumed, and a reset exactly one window duration
// after the read. Two reads 67 minutes apart both reported reset_at =
// captured_at + 300m, so the value tracks the read instead of marking a
// boundary.
func idleUsageFixture(now time.Time) []byte {
	body, err := json.Marshal(map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": 18000,
				"used_percent":         0,
				"reset_at":             now.Add(5 * time.Hour).Unix(),
			},
			"secondary_window": map[string]any{
				"limit_window_seconds": 604800,
				"used_percent":         31,
				"reset_at":             now.Add(4 * 24 * time.Hour).Unix(),
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

// The preheat exists to open a window while the account is idle. Reading the
// idle placeholder as a well-stocked open window skipped every occurrence in
// exactly that situation, so preheating could never do anything.
func TestIdleAccountProceedsInsteadOfSkippingAsSufficient(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 12, 54, 0, time.UTC)
	h := newQuotaHost(now)
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: idleUsageFixture(now)}
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(domain.DefaultConfig(), account.Key, now); got != domain.DecisionProceed {
		t.Fatalf("decision = %q, want %q: an idle account is the one case a preheat must run", got, domain.DecisionProceed)
	}
}

// The complementary case must keep working: a window that is genuinely running
// with quota to spare is still not worth a request.
func TestRunningWindowWithHeadroomStillSkips(t *testing.T) {
	now := time.Date(2026, 9, 15, 4, 12, 54, 0, time.UTC)
	h := newQuotaHost(now)
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: usageFixture(now, 20, 20, true)}
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(domain.DefaultConfig(), account.Key, now); got != domain.DecisionSufficientWindow {
		t.Fatalf("decision = %q, want %q for a running window with headroom", got, domain.DecisionSufficientWindow)
	}
}
