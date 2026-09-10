package domain

import "time"

type ResetOutcome string

const (
	ResetPending   ResetOutcome = "pending"
	ResetSucceeded ResetOutcome = "succeeded"
	ResetFailed    ResetOutcome = "failed"
	ResetUnknown   ResetOutcome = "unknown"
)

type ResetAudit struct {
	IdempotencyKey         string       `json:"idempotency_key"`
	RequestedAt            time.Time    `json:"requested_at"`
	FinishedAt             time.Time    `json:"finished_at,omitempty"`
	AccountKey             string       `json:"account_key"`
	MaskedIdentity         string       `json:"masked_identity"`
	PriorApplicableCredits *int         `json:"prior_applicable_credits,omitempty"`
	Outcome                ResetOutcome `json:"outcome"`
	HTTPCategory           string       `json:"http_category,omitempty"`
	CorrelationID          string       `json:"correlation_id"`
}

// ResetHTTPResult is an internal upstream result. It deliberately has no
// JSON export path because its fields are not part of any management payload.
type ResetHTTPResult struct {
	StatusCode int    `json:"-"`
	Category   string `json:"-"`
}
