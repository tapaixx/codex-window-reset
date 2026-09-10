package domain

import "time"

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

type ResetCredit struct {
	ID        string    `json:"id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type UsageSnapshot struct {
	AccountKey           string        `json:"account_key"`
	CapturedAt           time.Time     `json:"captured_at"`
	Windows              []UsageWindow `json:"windows"`
	ResetCredits         []ResetCredit `json:"reset_credits,omitempty"`
	ResetApplicableCount *int          `json:"reset_applicable_count,omitempty"`
	ResetInfoComplete    bool          `json:"reset_info_complete"`
}

type SnapshotView struct {
	Snapshot         UsageSnapshot `json:"snapshot"`
	Stale            bool          `json:"stale"`
	LastAttemptAt    time.Time     `json:"last_attempt_at"`
	RefreshErrorCode ErrorCode     `json:"refresh_error_code,omitempty"`
}

type GuardrailHold struct {
	AccountKey    string    `json:"account_key"`
	EstablishedAt time.Time `json:"established_at"`
	FloorPercent  int       `json:"floor_percent"`
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
