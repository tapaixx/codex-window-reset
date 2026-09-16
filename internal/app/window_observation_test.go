package app

import (
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// Two live preheats opened windows at 05:31:43 and 05:38:27 and both were
// recorded as WindowUnchanged: the snapshot taken immediately after the
// request still showed the idle placeholder, because upstream usage had not
// caught up yet. That reports a preheat that did exactly its job as having
// opened nothing.
func TestWindowVerificationWaitsForUpstreamToCatchUp(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	fx.probes.ReleaseAll()

	previous := windowObservationDelays
	windowObservationDelays = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	defer func() { windowObservationDelays = previous }()

	captured := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	idle := domain.UsageSnapshot{
		AccountKey: "a", CapturedAt: captured,
		Windows: []domain.UsageWindow{{DurationMinutes: 300, RemainingPercent: 100, ResetAt: captured.Add(300 * time.Minute), Short: true}},
	}
	opened := domain.UsageSnapshot{
		AccountKey: "a", CapturedAt: captured,
		Windows: []domain.UsageWindow{{DurationMinutes: 300, RemainingPercent: 100, ResetAt: captured.Add(299 * time.Minute), Short: true}},
	}
	// Idle before the request, still idle on the first read afterwards, then the
	// window appears.
	fx.quota.snapshots["a"] = idle
	fx.quota.sequence = []domain.UsageSnapshot{idle, idle, opened}

	record := fx.runtime.ExecutePreheat(t.Context(), domain.PlannedOccurrence{ID: "occ-catchup", AccountKey: "a"})
	if record.RequestOutcome != domain.RequestSucceeded {
		t.Fatalf("probe outcome=%s, want succeeded", record.RequestOutcome)
	}
	if record.WindowOutcome != domain.WindowVerifiedStarted {
		t.Fatalf("window outcome=%s, want %s once upstream caught up", record.WindowOutcome, domain.WindowVerifiedStarted)
	}
}
