package domain

import (
	"testing"
	"time"
)

// Shape observed on a live account with no window running: remaining 100% and a
// reset exactly one duration ahead of the read. Two consecutive reads 67
// minutes apart both reported reset_at = captured_at + 300m, proving the value
// slides with the read rather than marking a real boundary.
func TestIdleAccountPlaceholderIsNotAnActiveWindow(t *testing.T) {
	for _, captured := range []time.Time{
		time.Date(2026, 9, 15, 4, 12, 54, 0, time.UTC),
		time.Date(2026, 9, 15, 5, 19, 28, 0, time.UTC),
	} {
		window := UsageWindow{DurationMinutes: 300, RemainingPercent: 100, ResetAt: captured.Add(300 * time.Minute), Short: true}
		if window.ActiveAt(captured) {
			t.Fatalf("idle placeholder at %s reported as an active window", captured)
		}
	}
}

func TestRunningWindowIsActive(t *testing.T) {
	captured := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		resetIn   time.Duration
		wantAlive bool
	}{
		{"just started", 299 * time.Minute, true},
		{"half consumed", 150 * time.Minute, true},
		{"about to reset", time.Minute, true},
		{"already reset", -time.Minute, false},
		{"full duration ahead", 300 * time.Minute, false},
		{"a hair over a full duration", 301 * time.Minute, false},
		{"started a minute ago", 299 * time.Minute, true},
		// Within the skew slack a just-started window and a placeholder are
		// indistinguishable. The tie resolves toward idle on purpose: a
		// needless preheat costs one cheap request, while a needless skip is
		// the defect this rule exists to prevent.
		{"started inside the skew slack", 300*time.Minute - 10*time.Second, false},
	}
	for _, testCase := range cases {
		window := UsageWindow{DurationMinutes: 300, RemainingPercent: 60, ResetAt: captured.Add(testCase.resetIn), Short: true}
		if got := window.ActiveAt(captured); got != testCase.wantAlive {
			t.Fatalf("%s: active=%t, want %t", testCase.name, got, testCase.wantAlive)
		}
	}
}

func TestActiveAtHandlesMissingData(t *testing.T) {
	captured := time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)
	if (UsageWindow{DurationMinutes: 300}).ActiveAt(captured) {
		t.Fatal("a window without a reset timestamp cannot be active")
	}
	if (UsageWindow{ResetAt: captured.Add(time.Hour)}).ActiveAt(captured) {
		t.Fatal("a window without a duration cannot be judged active")
	}
	// Fixtures that omit the capture time keep the previous permissive reading.
	if !(UsageWindow{DurationMinutes: 300, ResetAt: captured}).ActiveAt(time.Time{}) {
		t.Fatal("a snapshot without a capture time must not be treated as idle")
	}
}

// The weekly window of the same live account: genuinely running, four days from
// resetting, well inside its 7-day duration.
func TestLongRunningWeeklyWindowStaysActive(t *testing.T) {
	captured := time.Date(2026, 9, 15, 4, 12, 54, 0, time.UTC)
	window := UsageWindow{DurationMinutes: 10080, RemainingPercent: 41, ResetAt: captured.Add(6017 * time.Minute)}
	if !window.ActiveAt(captured) {
		t.Fatal("a running weekly window was reported idle")
	}
}
