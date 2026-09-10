package domain

import (
	"encoding/json"
	"time"
)

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

func (a ResetAudit) MarshalJSON() ([]byte, error) {
	type wire ResetAudit
	value := wire(a)
	value.RequestedAt = a.RequestedAt.UTC()
	value.FinishedAt = a.FinishedAt.UTC()
	return json.Marshal(value)
}

func (a *ResetAudit) UnmarshalJSON(data []byte) error {
	type wire ResetAudit
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*a = ResetAudit(value)
	a.RequestedAt = a.RequestedAt.UTC()
	a.FinishedAt = a.FinishedAt.UTC()
	return nil
}

// ResetHTTPResult is an internal upstream result. It deliberately has no
// JSON export path because its fields are not part of any management payload.
type ResetHTTPResult struct {
	StatusCode int    `json:"-"`
	Category   string `json:"-"`
}
