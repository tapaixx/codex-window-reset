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
				{"limitWindowSeconds": 7200, "usedPercent": 25, "resetAfterSeconds": 60},
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

// The unit belongs to the field name. Scaling any value in [0,1] by 100 cannot
// tell one percent from a full window, and a live response carrying
// used_percent 1 was reported as a fully consumed window on an account that
// had 99% of it left.
func TestParseUsageReadsTheUsageUnitFromTheFieldName(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		field     string
		value     string
		remaining int
	}{
		{"one percent used", "used_percent", "1", 99},
		{"thirty percent used", "used_percent", "30", 70},
		{"a fraction of a percent", "used_percent", "0.25", 100},
		{"fraction field is scaled", "used_fraction", "0.3", 70},
		{"a full fraction is a full window", "used_fraction", "1", 0},
		{"remaining stated directly", "remaining_percent", "1", 1},
		{"remaining as a fraction", "remaining_fraction", "0.25", 25},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":3600,"` + testCase.field + `":` + testCase.value + `}}}`)
			got, err := ParseUsage(raw, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Windows[0].RemainingPercent != testCase.remaining {
				t.Fatalf("%s=%s gave remaining %d, want %d", testCase.field, testCase.value, got.Windows[0].RemainingPercent, testCase.remaining)
			}
		})
	}
}

// The exact payload the Operator hit: a nearly untouched window reported as
// empty, with the reset timestamp rendering correctly beside it.
func TestParseUsageKeepsANearlyUntouchedWindowFull(t *testing.T) {
	now := time.Date(2026, 9, 15, 13, 46, 59, 0, time.UTC)
	raw := []byte(`{"rate_limit":{"allowed":true,"limit_reached":false,
		"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_at":1789493450},
		"secondary_window":{"used_percent":32,"limit_window_seconds":604800,"reset_at":1789815368}}}`)
	got, err := ParseUsage(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Windows[0].RemainingPercent != 99 {
		t.Fatalf("short window remaining = %d, want 99", got.Windows[0].RemainingPercent)
	}
	if got.Windows[1].RemainingPercent != 68 {
		t.Fatalf("long window remaining = %d, want 68", got.Windows[1].RemainingPercent)
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

func TestParseUsageRejectsInvalidUsageAndRemainingRanges(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "negative used percent", raw: `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":-1}}}`},
		{name: "used percent above one hundred", raw: `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":101}}}`},
		{name: "negative remaining percent", raw: `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"remaining_percent":-1}}}`},
		{name: "remaining percent above one hundred", raw: `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"remaining_percent":101}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseUsage([]byte(tt.raw), now); err == nil {
				t.Fatal("ParseUsage accepted an out-of-range usage value")
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
