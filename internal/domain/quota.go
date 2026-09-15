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

// windowStartSlack absorbs clock skew between the snapshot's capture time and
// the upstream's reset timestamp. A window that started less than this ago is
// still treated as active.
const windowStartSlack = 30 * time.Second

// ActiveAt reports whether a usage window is actually running at the moment the
// snapshot was captured.
//
// The upstream does not say "no window is open". For an idle account it reports
// a full window that has not started: remaining 100% with reset_at exactly one
// window duration ahead of the read, and that reset_at slides forward with
// every read. Treating that as an open window is what made preheating
// impossible — the only moment a preheat request can start a window is the
// moment the account is idle, which is precisely when the placeholder appears.
//
// A genuinely running window started in the past, so strictly less than one
// full duration remains before it resets.
func (w UsageWindow) ActiveAt(capturedAt time.Time) bool {
	if w.ResetAt.IsZero() || w.DurationMinutes <= 0 {
		return false
	}
	if capturedAt.IsZero() {
		return true
	}
	remaining := w.ResetAt.UTC().Sub(capturedAt.UTC())
	if remaining <= 0 {
		return false
	}
	return remaining+windowStartSlack < time.Duration(w.DurationMinutes)*time.Minute
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
	AccountKey string        `json:"account_key"`
	CapturedAt time.Time     `json:"captured_at"`
	Windows    []UsageWindow `json:"windows"`
	// LimitReached is the upstream's own enforcement verdict, which is not the
	// same fact as a used percentage. The two can disagree — a window can read
	// as fully consumed while the account is still allowed to send requests —
	// and inferring availability from the percentage alone reports the account
	// as out of quota when upstream says it is not.
	LimitReached bool   `json:"limit_reached,omitempty"`
	ReachedType  string `json:"rate_limit_reached_type,omitempty"`
	// AvailableAt is set only when upstream says the probe-relevant model is
	// unavailable until a given time.
	AvailableAt          time.Time     `json:"available_at,omitempty"`
	ResetCredits         []ResetCredit `json:"reset_credits,omitempty"`
	ResetApplicableCount *int          `json:"reset_applicable_count,omitempty"`
	ResetInfoComplete    bool          `json:"reset_info_complete"`
}

func (s UsageSnapshot) MarshalJSON() ([]byte, error) {
	type wire UsageSnapshot
	value := wire(s)
	value.CapturedAt = s.CapturedAt.UTC()
	value.AvailableAt = s.AvailableAt.UTC()
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
	DecisionProceed QuotaDecision = "proceed"
	// DecisionSufficientWindow is retained for history written before the
	// decision became "a window is already running". It is no longer produced.
	DecisionSufficientWindow QuotaDecision = "sufficient_window"
	DecisionWindowActive     QuotaDecision = "window_active"
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
