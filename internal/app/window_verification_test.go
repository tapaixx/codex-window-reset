package app

import (
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func snapshotAt(captured time.Time, resetIn time.Duration, remaining int) *domain.UsageSnapshot {
	return &domain.UsageSnapshot{
		CapturedAt: captured,
		Windows:    []domain.UsageWindow{{DurationMinutes: 300, RemainingPercent: remaining, ResetAt: captured.Add(resetIn), Short: true}},
	}
}

// An idle account's reset timestamp slides forward with every read, so the two
// snapshots around a preheat always differ. Treating that difference as proof
// that a window opened would report success for a request that did nothing.
func TestWindowVerificationIgnoresTheSlidingIdlePlaceholder(t *testing.T) {
	start := time.Date(2026, 9, 15, 2, 30, 0, 0, time.UTC)
	before := snapshotAt(start, 300*time.Minute, 100)
	stillIdle := snapshotAt(start.Add(2*time.Second), 300*time.Minute, 100)
	if got := classifyWindow(domain.RequestSucceeded, before, stillIdle); got != domain.WindowUnchanged {
		t.Fatalf("window outcome = %q, want %q: nothing was opened", got, domain.WindowUnchanged)
	}
}

func TestWindowVerificationConfirmsAWindowThatActuallyOpened(t *testing.T) {
	start := time.Date(2026, 9, 15, 2, 30, 0, 0, time.UTC)
	before := snapshotAt(start, 300*time.Minute, 100)
	// The request opened a window, so the reset is now anchored to the request
	// rather than sliding with the read.
	opened := snapshotAt(start.Add(2*time.Second), 300*time.Minute-2*time.Minute, 99)
	if got := classifyWindow(domain.RequestSucceeded, before, opened); got != domain.WindowVerifiedStarted {
		t.Fatalf("window outcome = %q, want %q", got, domain.WindowVerifiedStarted)
	}
}

func TestWindowVerificationReportsAnAlreadyRunningWindow(t *testing.T) {
	start := time.Date(2026, 9, 15, 2, 30, 0, 0, time.UTC)
	before := snapshotAt(start, 90*time.Minute, 40)
	after := snapshotAt(start.Add(2*time.Second), 90*time.Minute, 39)
	if got := classifyWindow(domain.RequestSucceeded, before, after); got != domain.WindowAlreadyActive {
		t.Fatalf("window outcome = %q, want %q", got, domain.WindowAlreadyActive)
	}
}

func TestFailedRequestNeverVerifiesAWindow(t *testing.T) {
	start := time.Date(2026, 9, 15, 2, 30, 0, 0, time.UTC)
	before := snapshotAt(start, 300*time.Minute, 100)
	after := snapshotAt(start.Add(2*time.Second), 298*time.Minute, 99)
	if got := classifyWindow(domain.RequestTimeout, before, after); got != domain.WindowNotObserved {
		t.Fatalf("window outcome = %q, want %q", got, domain.WindowNotObserved)
	}
}
