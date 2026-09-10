package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func TestPreheatGuardrailAndSufficientDecisionsSkipProbe(t *testing.T) {
	tests := []struct {
		name     string
		snapshot domain.UsageSnapshot
		want     domain.QuotaDecision
	}{
		{
			name: "guardrail",
			snapshot: domain.UsageSnapshot{AccountKey: "a", CapturedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), Windows: []domain.UsageWindow{
				{DurationMinutes: 300, RemainingPercent: 80, ResetAt: time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC), Short: true},
				{DurationMinutes: 1000, RemainingPercent: 5, ResetAt: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
			}},
			want: domain.DecisionGuardrailHold,
		},
		{
			name: "sufficient",
			snapshot: domain.UsageSnapshot{AccountKey: "a", CapturedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), Windows: []domain.UsageWindow{
				{DurationMinutes: 300, RemainingPercent: 80, ResetAt: time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC), Short: true},
			}},
			want: domain.DecisionSufficientWindow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTask7Fixture(t, accounts.Account{Key: "a"})
			defer fx.runtime.Stop()
			fx.quota.snapshots["a"] = tc.snapshot
			record := fx.runtime.ExecutePreheat(context.Background(), domain.PlannedOccurrence{ID: "occ-" + tc.name, AccountKey: "a"})
			if record.Decision != tc.want {
				t.Fatalf("decision=%s, want %s", record.Decision, tc.want)
			}
			if got := fx.probes.Calls(); got != 0 {
				t.Fatalf("probe calls=%d, want 0", got)
			}
		})
	}
}

func TestPreheatQuotaUnknownFailsOpenAndRecordsIndependentOutcomes(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	fx.quota.errors["a"] = errors.New("quota unavailable")
	fx.probes.ReleaseAll()
	record := fx.runtime.ExecutePreheat(context.Background(), domain.PlannedOccurrence{ID: "occ-a", AccountKey: "a"})
	if record.Decision != domain.DecisionUnknownFailOpen {
		t.Fatalf("decision=%s", record.Decision)
	}
	if record.RequestOutcome != domain.RequestSucceeded {
		t.Fatalf("request=%s", record.RequestOutcome)
	}
	if record.WindowOutcome != domain.WindowUnverified {
		t.Fatalf("window=%s", record.WindowOutcome)
	}
	if record.ErrorCode != domain.CodeWindowUnverified {
		t.Fatalf("error code=%s", record.ErrorCode)
	}
}

func TestFallbackDecisionDoesNotAuthorizeStaleOrUntimestampedSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		capturedAt time.Time
	}{
		{name: "stale", capturedAt: now.Add(-6 * time.Minute)},
		{name: "untimestamped", capturedAt: time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTask7Fixture(t, accounts.Account{Key: "a"})
			defer fx.runtime.Stop()
			fx.quota.snapshots["a"] = domain.UsageSnapshot{
				AccountKey: tc.name,
				CapturedAt: tc.capturedAt,
				Windows: []domain.UsageWindow{{
					DurationMinutes:  300,
					RemainingPercent: 80,
					ResetAt:          now.Add(3 * time.Hour),
					Short:            true,
				}},
			}
			fx.probes.ReleaseAll()

			record := fx.runtime.ExecutePreheat(context.Background(), domain.PlannedOccurrence{ID: "occ-" + tc.name, AccountKey: "a"})
			if record.Decision != domain.DecisionProceed {
				t.Fatalf("decision=%s, want %s", record.Decision, domain.DecisionProceed)
			}
			if got := fx.probes.Calls(); got != 1 {
				t.Fatalf("probe calls=%d, want 1", got)
			}
		})
	}
}

func TestPreheatCompensationOnlyForEligibleAutomaticFailures(t *testing.T) {
	tests := []struct {
		name    string
		result  domain.ProbeResult
		wantDue bool
	}{
		{name: "network", result: domain.ProbeResult{Outcome: domain.RequestNetworkError}, wantDue: true},
		{name: "timeout", result: domain.ProbeResult{Outcome: domain.RequestTimeout}, wantDue: true},
		{name: "rate limited", result: domain.ProbeResult{Outcome: domain.RequestRateLimited, HTTPStatus: 429}, wantDue: true},
		{name: "server", result: domain.ProbeResult{Outcome: domain.RequestUpstreamError, HTTPStatus: 503}, wantDue: true},
		{name: "unauthorized", result: domain.ProbeResult{Outcome: domain.RequestUnauthorized, HTTPStatus: 401}, wantDue: false},
		{name: "validation", result: domain.ProbeResult{Outcome: domain.RequestResponseError}, wantDue: false},
		{name: "unexpected output", result: domain.ProbeResult{Outcome: domain.RequestUnexpectedOutput}, wantDue: false},
		{name: "success unverified", result: domain.ProbeResult{Outcome: domain.RequestSucceeded}, wantDue: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTask7Fixture(t, accounts.Account{Key: "a"})
			defer fx.runtime.Stop()
			fx.probes.result = tc.result
			fx.probes.ReleaseAll()
			occurrence := domain.PlannedOccurrence{ID: "occ-" + tc.name, AccountKey: "a"}
			record := fx.runtime.ExecutePreheat(context.Background(), occurrence)
			if record.RequestOutcome != tc.result.Outcome {
				t.Fatalf("request=%s, want %s", record.RequestOutcome, tc.result.Outcome)
			}
			state, ok := fx.state.Occurrence(occurrence.ID)
			if !ok {
				t.Fatal("occurrence state not persisted")
			}
			if got := !state.CompensationDueAt.IsZero(); got != tc.wantDue {
				t.Fatalf("due=%s, want due=%t", state.CompensationDueAt, tc.wantDue)
			}
		})
	}
}
