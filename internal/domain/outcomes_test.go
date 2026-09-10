package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJSONEnumValuesRemainStable(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "request succeeded", value: RequestSucceeded, want: "succeeded"},
		{name: "request unauthorized", value: RequestUnauthorized, want: "unauthorized"},
		{name: "request forbidden", value: RequestForbidden, want: "forbidden"},
		{name: "request payment required", value: RequestPaymentRequired, want: "payment_required"},
		{name: "request rate limited", value: RequestRateLimited, want: "rate_limited"},
		{name: "request upstream error", value: RequestUpstreamError, want: "upstream_error"},
		{name: "request network error", value: RequestNetworkError, want: "network_error"},
		{name: "request timeout", value: RequestTimeout, want: "timeout"},
		{name: "request response error", value: RequestResponseError, want: "response_error"},
		{name: "request unexpected output", value: RequestUnexpectedOutput, want: "unexpected_output"},
		{name: "request credential error", value: RequestCredentialError, want: "credential_error"},
		{name: "request disabled", value: RequestDisabled, want: "disabled"},
		{name: "window verified started", value: WindowVerifiedStarted, want: "verified_started"},
		{name: "window already active", value: WindowAlreadyActive, want: "already_active"},
		{name: "window unchanged", value: WindowUnchanged, want: "unchanged"},
		{name: "window unverified", value: WindowUnverified, want: "unverified"},
		{name: "window not observed", value: WindowNotObserved, want: "not_observed"},
		{name: "decision proceed", value: DecisionProceed, want: "proceed"},
		{name: "decision sufficient window", value: DecisionSufficientWindow, want: "sufficient_window"},
		{name: "decision guardrail hold", value: DecisionGuardrailHold, want: "guardrail_hold"},
		{name: "decision unknown fail open", value: DecisionUnknownFailOpen, want: "quota_unknown_fail_open"},
		{name: "trigger health probe", value: TriggerHealthProbe, want: "health_probe"},
		{name: "trigger preheat", value: TriggerPreheat, want: "preheat"},
		{name: "trigger compensation", value: TriggerCompensation, want: "compensation"},
		{name: "occurrence planned", value: OccurrencePlanned, want: "planned"},
		{name: "occurrence running", value: OccurrenceRunning, want: "running"},
		{name: "occurrence succeeded", value: OccurrenceSucceeded, want: "succeeded"},
		{name: "occurrence failed", value: OccurrenceFailed, want: "failed"},
		{name: "occurrence skipped", value: OccurrenceSkipped, want: "skipped"},
		{name: "occurrence missed", value: OccurrenceMissed, want: "missed"},
		{name: "reset pending", value: ResetPending, want: "pending"},
		{name: "reset succeeded", value: ResetSucceeded, want: "succeeded"},
		{name: "reset failed", value: ResetFailed, want: "failed"},
		{name: "reset unknown", value: ResetUnknown, want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("encoded %s as %q, want %q", encoded, got, tt.want)
			}
		})
	}
}

func TestAccountIdentityJSONOnlyExposesSafeProjection(t *testing.T) {
	view := AccountIdentity{AccountKey: "stable-key", MaskedIdentity: "a***@example.com", PlanLabel: "Pro"}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]bool{"account_key": true, "masked_identity": true, "plan_label": true}
	if len(fields) != len(wantFields) {
		t.Fatalf("identity projection fields = %v, want only %v", fields, wantFields)
	}
	for field := range fields {
		if !wantFields[field] {
			t.Fatalf("identity projection exposed unexpected field %q: %s", field, encoded)
		}
	}
}

func TestResetHTTPResultHasNoJSONExportPath(t *testing.T) {
	encoded, err := json.Marshal(ResetHTTPResult{StatusCode: 200, Category: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, []byte("{}")) {
		t.Fatalf("reset HTTP result exported JSON fields: %s", encoded)
	}
}

func TestJSONTimeFieldsNormalizeToUTC(t *testing.T) {
	// This instant is intentionally represented with a non-UTC offset. Every
	// JSON-facing domain record should expose the same UTC RFC3339 instant.
	const wantUTC = "2026-01-02T03:04:05.123456789Z"
	const wantOffset = "+08:00"
	offset := time.FixedZone("operator-offset", 8*60*60)
	instant := time.Date(2026, time.January, 2, 11, 4, 5, 123456789, offset)
	credits := 2
	window := UsageWindow{DurationMinutes: 300, RemainingPercent: 80, ResetAt: instant, Short: true}
	planned := PlannedOccurrence{
		ID:          "occurrence",
		AccountKey:  "account",
		LocalDate:   "2026-01-02",
		PeriodIndex: 0,
		WindowStart: instant,
		WindowEnd:   instant,
		PlannedAt:   instant,
	}

	cases := []struct {
		name  string
		value any
	}{
		{name: "usage window", value: window},
		{name: "reset credit", value: ResetCredit{ID: "credit", ExpiresAt: instant}},
		{name: "usage snapshot", value: UsageSnapshot{AccountKey: "account", CapturedAt: instant, Windows: []UsageWindow{window}, ResetCredits: []ResetCredit{{ID: "credit", ExpiresAt: instant}}, ResetApplicableCount: &credits, ResetInfoComplete: true}},
		{name: "snapshot view", value: SnapshotView{Snapshot: UsageSnapshot{AccountKey: "account", CapturedAt: instant, Windows: []UsageWindow{window}}, Stale: false, LastAttemptAt: instant}},
		{name: "guardrail hold", value: GuardrailHold{AccountKey: "account", EstablishedAt: instant, FloorPercent: 10}},
		{name: "operation", value: OperationRecord{ID: "operation", CorrelationID: "correlation", Trigger: TriggerPreheat, AccountKey: "account", MaskedIdentity: "a***@example.com", StartedAt: instant, FinishedAt: instant, RequestOutcome: RequestSucceeded, WindowOutcome: WindowVerifiedStarted, LatencyMS: 1}},
		{name: "planned occurrence", value: planned},
		{name: "occurrence state", value: OccurrenceState{PlannedOccurrence: planned, Status: OccurrencePlanned, CompensationDueAt: instant}},
		{name: "runtime state", value: RuntimeState{SchemaVersion: 1, Occurrences: map[string]OccurrenceState{"occurrence": {PlannedOccurrence: planned, Status: OccurrencePlanned}}, NextRuns: map[string]time.Time{"occurrence": instant}, GuardrailHolds: map[string]GuardrailHold{"account": {AccountKey: "account", EstablishedAt: instant, FloorPercent: 10}}}},
		{name: "reset audit", value: ResetAudit{IdempotencyKey: "idempotency", RequestedAt: instant, FinishedAt: instant, AccountKey: "account", MaskedIdentity: "a***@example.com", PriorApplicableCredits: &credits, Outcome: ResetSucceeded, CorrelationID: "correlation"}},
		{name: "timeline segment", value: TimelineSegment{Kind: "work", Start: instant, End: instant, AccountKey: "account"}},
		{name: "simulation result", value: SimulationResult{WorkMinutes: 1, PreheatWindows: []PlannedOccurrence{planned}, TimelineSegments: []TimelineSegment{{Kind: "work", Start: instant, End: instant}}}},
		{name: "status view", value: StatusView{Enabled: true, NextRuns: map[string]time.Time{"occurrence": instant}, RunID: "run", RunTotal: 1, RunCompleted: 1}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), wantUTC) {
				t.Fatalf("JSON did not expose UTC instant %q: %s", wantUTC, encoded)
			}
			if strings.Contains(string(encoded), wantOffset) {
				t.Fatalf("JSON retained non-UTC offset %q: %s", wantOffset, encoded)
			}
		})
	}
}
