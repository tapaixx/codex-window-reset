package domain

import (
	"encoding/json"
	"time"
)

type RequestOutcome string

const (
	RequestSucceeded        RequestOutcome = "succeeded"
	RequestUnauthorized     RequestOutcome = "unauthorized"
	RequestForbidden        RequestOutcome = "forbidden"
	RequestPaymentRequired  RequestOutcome = "payment_required"
	RequestRateLimited      RequestOutcome = "rate_limited"
	RequestUpstreamError    RequestOutcome = "upstream_error"
	RequestNetworkError     RequestOutcome = "network_error"
	RequestTimeout          RequestOutcome = "timeout"
	RequestResponseError    RequestOutcome = "response_error"
	RequestUnexpectedOutput RequestOutcome = "unexpected_output"
	RequestCredentialError  RequestOutcome = "credential_error"
	RequestDisabled         RequestOutcome = "disabled"
)

type WindowOutcome string

const (
	WindowVerifiedStarted WindowOutcome = "verified_started"
	WindowAlreadyActive   WindowOutcome = "already_active"
	WindowUnchanged       WindowOutcome = "unchanged"
	WindowUnverified      WindowOutcome = "unverified"
	WindowNotObserved     WindowOutcome = "not_observed"
)

type UsageWindow struct {
	DurationMinutes  int       `json:"duration_minutes"`
	RemainingPercent int       `json:"remaining_percent"`
	ResetAt          time.Time `json:"reset_at,omitempty"`
	Short            bool      `json:"short"`
}

func (w UsageWindow) MarshalJSON() ([]byte, error) {
	type wire UsageWindow
	value := wire(w)
	value.ResetAt = w.ResetAt.UTC()
	return json.Marshal(value)
}

func (w *UsageWindow) UnmarshalJSON(data []byte) error {
	type wire UsageWindow
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*w = UsageWindow(value)
	w.ResetAt = w.ResetAt.UTC()
	return nil
}

type ResetCredit struct {
	ID        string    `json:"id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func (c ResetCredit) MarshalJSON() ([]byte, error) {
	type wire ResetCredit
	value := wire(c)
	value.ExpiresAt = c.ExpiresAt.UTC()
	return json.Marshal(value)
}

func (c *ResetCredit) UnmarshalJSON(data []byte) error {
	type wire ResetCredit
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = ResetCredit(value)
	c.ExpiresAt = c.ExpiresAt.UTC()
	return nil
}

type UsageSnapshot struct {
	AccountKey           string        `json:"account_key"`
	CapturedAt           time.Time     `json:"captured_at"`
	Windows              []UsageWindow `json:"windows"`
	ResetCredits         []ResetCredit `json:"reset_credits,omitempty"`
	ResetApplicableCount *int          `json:"reset_applicable_count,omitempty"`
	ResetInfoComplete    bool          `json:"reset_info_complete"`
}

func (s UsageSnapshot) MarshalJSON() ([]byte, error) {
	type wire UsageSnapshot
	value := wire(s)
	value.CapturedAt = s.CapturedAt.UTC()
	return json.Marshal(value)
}

func (s *UsageSnapshot) UnmarshalJSON(data []byte) error {
	type wire UsageSnapshot
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = UsageSnapshot(value)
	s.CapturedAt = s.CapturedAt.UTC()
	return nil
}

type SnapshotView struct {
	Snapshot         UsageSnapshot `json:"snapshot"`
	Stale            bool          `json:"stale"`
	LastAttemptAt    time.Time     `json:"last_attempt_at"`
	RefreshErrorCode ErrorCode     `json:"refresh_error_code,omitempty"`
}

func (s SnapshotView) MarshalJSON() ([]byte, error) {
	type wire SnapshotView
	value := wire(s)
	value.LastAttemptAt = s.LastAttemptAt.UTC()
	return json.Marshal(value)
}

func (s *SnapshotView) UnmarshalJSON(data []byte) error {
	type wire SnapshotView
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = SnapshotView(value)
	s.LastAttemptAt = s.LastAttemptAt.UTC()
	return nil
}

type GuardrailHold struct {
	AccountKey    string    `json:"account_key"`
	EstablishedAt time.Time `json:"established_at"`
	FloorPercent  int       `json:"floor_percent"`
}

func (h GuardrailHold) MarshalJSON() ([]byte, error) {
	type wire GuardrailHold
	value := wire(h)
	value.EstablishedAt = h.EstablishedAt.UTC()
	return json.Marshal(value)
}

func (h *GuardrailHold) UnmarshalJSON(data []byte) error {
	type wire GuardrailHold
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*h = GuardrailHold(value)
	h.EstablishedAt = h.EstablishedAt.UTC()
	return nil
}

type QuotaDecision string

const (
	DecisionProceed          QuotaDecision = "proceed"
	DecisionSufficientWindow QuotaDecision = "sufficient_window"
	DecisionGuardrailHold    QuotaDecision = "guardrail_hold"
	DecisionUnknownFailOpen  QuotaDecision = "quota_unknown_fail_open"
)

type ProbeResult struct {
	Outcome       RequestOutcome `json:"request_outcome"`
	HTTPStatus    int            `json:"http_status,omitempty"`
	LatencyMS     int64          `json:"latency_ms"`
	ErrorCode     ErrorCode      `json:"error_code,omitempty"`
	RetryEligible bool           `json:"retry_eligible"`
}
