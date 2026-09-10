package quota

import (
	"strings"
	"testing"
	"time"
)

func TestParseUsageSelectsShortestWindow(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":30,"reset_at":1788973200},"secondary_window":{"limit_window_seconds":604800,"used_percent":91,"reset_at":1789578000}},"rate_limit_reset_credits":{"available":2}}`)

	got, err := ParseUsage(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountKey != "" {
		t.Fatalf("account key = %q, want empty before service association", got.AccountKey)
	}
	if !got.CapturedAt.Equal(now) || got.CapturedAt.Location() != time.UTC {
		t.Fatalf("captured at = %#v, want UTC %s", got.CapturedAt, now)
	}
	if len(got.Windows) != 2 {
		t.Fatalf("windows = %#v, want two windows", got.Windows)
	}
	if got.Windows[0].DurationMinutes != 300 || got.Windows[0].RemainingPercent != 70 {
		t.Fatalf("short: %#v", got.Windows[0])
	}
	if !got.Windows[0].Short || got.Windows[1].Short {
		t.Fatalf("classification: %#v", got.Windows)
	}
	if got.ResetApplicableCount == nil || *got.ResetApplicableCount != 2 {
		t.Fatalf("credits: %#v", got)
	}
	if got.ResetInfoComplete {
		t.Fatal("embedded reset credits cannot make reset info complete")
	}
}

func TestParseUsageAcceptsCamelCaseAndArbitraryWindowLists(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"rateLimit": {
			"windows": [
				{"limitWindowSeconds": 7200, "usedPercent": 0.25, "resetAfterSeconds": 60},
				{"limitWindowSeconds": 18000, "usedPercent": 25, "resetAt": "1788973200"},
				{"limitWindowSeconds": 604800, "usedPercent": 50, "resetAt": "2026-09-16T12:00:00Z"}
			],
			"unrelated": {"allowed": true}
		},
		"rateLimitResetCredits": {"availableCount": 4}
	}`)

	got, err := ParseUsage(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Windows) != 3 {
		t.Fatalf("windows = %#v, want all three recognized windows", got.Windows)
	}
	if got.Windows[0].DurationMinutes != 120 || got.Windows[0].RemainingPercent != 75 || !got.Windows[0].Short {
		t.Fatalf("shortest camel-case window = %#v", got.Windows[0])
	}
	if got.Windows[1].DurationMinutes != 300 || got.Windows[1].RemainingPercent != 75 || got.Windows[1].Short {
		t.Fatalf("middle window = %#v", got.Windows[1])
	}
	if got.Windows[2].DurationMinutes != 10080 || got.Windows[2].RemainingPercent != 50 || got.Windows[2].Short {
		t.Fatalf("long window = %#v", got.Windows[2])
	}
	if got.Windows[0].ResetAt.Sub(now) != time.Minute {
		t.Fatalf("reset-after boundary = %s, want 1m", got.Windows[0].ResetAt.Sub(now))
	}
	if got.ResetApplicableCount == nil || *got.ResetApplicableCount != 4 {
		t.Fatalf("reset count = %#v, want 4", got.ResetApplicableCount)
	}
}

func TestParseUsageAcceptsFractionAndPercentUsageEquivalently(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{"rate_limit":{"fraction_window":{"limit_window_seconds":3600,"used_percent":0.3},"percent_window":{"limit_window_seconds":7200,"used_percent":30}}}`)

	got, err := ParseUsage(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Windows) != 2 {
		t.Fatalf("windows = %#v", got.Windows)
	}
	if got.Windows[0].RemainingPercent != 70 || got.Windows[1].RemainingPercent != 70 {
		t.Fatalf("fraction/percent conversion = %#v", got.Windows)
	}
}

func TestParseUsageAllowsMissingResetBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":10},"secondary_window":{"limit_window_seconds":604800,"used_percent":20}}}`)

	got, err := ParseUsage(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Windows) != 2 || !got.Windows[0].ResetAt.IsZero() || !got.Windows[1].ResetAt.IsZero() {
		t.Fatalf("missing reset boundaries = %#v", got.Windows)
	}
}

func TestParseUsageRejectsInvalidOrUnrecognizedPayloads(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "malformed json", raw: `{"rate_limit":`},
		{name: "missing rate limit", raw: `{}`},
		{name: "missing usage", raw: `{"rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`},
		{name: "non-positive duration", raw: `{"rate_limit":{"primary_window":{"limit_window_seconds":0,"used_percent":10}}}`},
		{name: "unrelated nested objects", raw: `{"rate_limit":{"metadata":{"limit_window_seconds":18000,"allowed":true}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseUsage([]byte(tt.raw), now); err == nil {
				t.Fatal("ParseUsage accepted an invalid or unrecognized payload")
			}
		})
	}
}

func TestParseUsageParsesNumericAndRFC3339ResetBoundariesAsUTC(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":10,"reset_at":"1788973200000"},"secondary_window":{"limit_window_seconds":604800,"used_percent":20,"reset_at":"2026-09-16T20:00:00+08:00"}}}`)

	got, err := ParseUsage(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Windows[0].ResetAt.Location() != time.UTC || got.Windows[1].ResetAt.Location() != time.UTC {
		t.Fatalf("reset boundary locations = %s, %s", got.Windows[0].ResetAt.Location(), got.Windows[1].ResetAt.Location())
	}
	if got.Windows[0].ResetAt.UnixMilli() != 1788973200000 {
		t.Fatalf("millisecond reset boundary = %s", got.Windows[0].ResetAt)
	}
	if got.Windows[1].ResetAt.Hour() != 12 {
		t.Fatalf("RFC3339 reset boundary = %s, want UTC noon", got.Windows[1].ResetAt)
	}
}

func TestParseUsageRejectsEmptyInputWithoutLeakingBody(t *testing.T) {
	secret := "fixture-secret-token"
	_, err := ParseUsage([]byte(strings.Repeat(secret, 2)), time.Now())
	if err == nil {
		t.Fatal("ParseUsage accepted non-JSON input")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("parse error leaked input: %v", err)
	}
}
