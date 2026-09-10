package app

import (
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func task7Snapshot(resetAt time.Time, active bool) *domain.UsageSnapshot {
	capturedAt := resetAt
	remaining := 0
	if active {
		capturedAt = resetAt.Add(-time.Minute)
		remaining = 80
	}
	return &domain.UsageSnapshot{
		AccountKey: "a",
		CapturedAt: capturedAt,
		Windows: []domain.UsageWindow{{
			DurationMinutes:  300,
			RemainingPercent: remaining,
			ResetAt:          resetAt,
			Short:            true,
		}},
	}
}

func TestClassifyWindowOutcome(t *testing.T) {
	resetA := time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC)
	resetB := resetA.Add(5 * time.Hour)
	cases := []struct {
		name          string
		request       domain.RequestOutcome
		before, after *domain.UsageSnapshot
		want          domain.WindowOutcome
	}{
		{"new boundary", domain.RequestSucceeded, task7Snapshot(resetA, false), task7Snapshot(resetB, true), domain.WindowVerifiedStarted},
		{"active before", domain.RequestSucceeded, task7Snapshot(resetA, true), task7Snapshot(resetA, true), domain.WindowAlreadyActive},
		{"same inactive boundary", domain.RequestSucceeded, task7Snapshot(resetA, false), task7Snapshot(resetA, false), domain.WindowUnchanged},
		{"missing after", domain.RequestSucceeded, task7Snapshot(resetA, false), nil, domain.WindowUnverified},
		{"request failed", domain.RequestRateLimited, task7Snapshot(resetA, false), nil, domain.WindowNotObserved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyWindow(tc.request, tc.before, tc.after); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
