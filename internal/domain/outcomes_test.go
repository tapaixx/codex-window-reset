package domain

import (
	"bytes"
	"encoding/json"
	"testing"
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
