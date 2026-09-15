package quota

import (
	"testing"
	"time"
)

// Captured from a live chatgpt.com/backend-api/wham/usage response. The
// enforcement verdict travels beside the percentages and is a different fact:
// upstream can report a fully consumed window while still allowing requests.
const liveUsageBody = `{
  "account_id": "541773e7-d581-469c-87fe-eabbca968d01",
  "plan_type": "team",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_after_seconds": 10507, "reset_at": 1789472897},
    "secondary_window": {"used_percent": 61, "limit_window_seconds": 604800, "reset_after_seconds": 348224, "reset_at": 1789810564}
  },
  "rate_limit_reached_type": null,
  "model_usage": {"gpt-6-astra": {"available": true, "available_at": null}},
  "rate_limit_reset_credits": {"available_count": 1, "applicable_available_count": 0}
}`

func TestSnapshotKeepsUpstreamEnforcementVerdictBesideThePercentage(t *testing.T) {
	captured := time.Date(2026, 9, 15, 8, 53, 10, 0, time.UTC)
	snapshot, err := ParseUsage([]byte(liveUsageBody), captured)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Windows[0].RemainingPercent; got != 0 {
		t.Fatalf("remaining=%d, want 0 for a fully consumed window", got)
	}
	// The percentage says empty; upstream says the account may still be used.
	// Losing this distinction reports an account as out of quota when it is not.
	if snapshot.LimitReached {
		t.Fatal("limit_reached=false was dropped, leaving only the percentage")
	}
	if snapshot.ReachedType != "" {
		t.Fatalf("reached type=%q, want empty", snapshot.ReachedType)
	}
	if !snapshot.AvailableAt.IsZero() {
		t.Fatalf("available_at=%s, want zero while the model is available", snapshot.AvailableAt)
	}
}

func TestSnapshotRecordsAnEnforcedLimitAndItsRecoveryTime(t *testing.T) {
	body := `{
	  "rate_limit": {
	    "allowed": false,
	    "limit_reached": true,
	    "primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_after_seconds": 600},
	    "secondary_window": {"used_percent": 61, "limit_window_seconds": 604800, "reset_after_seconds": 348224}
	  },
	  "rate_limit_reached_type": "primary",
	  "model_usage": {
	    "gpt-6-astra": {"available": false, "available_at": "2026-09-15T09:30:00Z"},
	    "gpt-7-nova": {"available": false, "available_at": "2026-09-15T09:10:00Z"},
	    "gpt-5-luna": {"available": true, "available_at": null}
	  }
	}`
	captured := time.Date(2026, 9, 15, 8, 53, 10, 0, time.UTC)
	snapshot, err := ParseUsage([]byte(body), captured)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.LimitReached {
		t.Fatal("an enforced limit was not recorded")
	}
	if snapshot.ReachedType != "primary" {
		t.Fatalf("reached type=%q, want primary", snapshot.ReachedType)
	}
	want := time.Date(2026, 9, 15, 9, 10, 0, 0, time.UTC)
	if !snapshot.AvailableAt.Equal(want) {
		t.Fatalf("available_at=%s, want the soonest unavailable model %s", snapshot.AvailableAt, want)
	}
}
