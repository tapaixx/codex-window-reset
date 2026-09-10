package domain

import "time"

type OperationTrigger string

const (
	TriggerHealthProbe  OperationTrigger = "health_probe"
	TriggerPreheat      OperationTrigger = "preheat"
	TriggerCompensation OperationTrigger = "compensation"
)

type OperationRecord struct {
	ID             string           `json:"id"`
	CorrelationID  string           `json:"correlation_id"`
	Trigger        OperationTrigger `json:"trigger"`
	OccurrenceID   string           `json:"occurrence_id,omitempty"`
	AccountKey     string           `json:"account_key"`
	MaskedIdentity string           `json:"masked_identity"`
	StartedAt      time.Time        `json:"started_at"`
	FinishedAt     time.Time        `json:"finished_at"`
	RequestOutcome RequestOutcome   `json:"request_outcome"`
	WindowOutcome  WindowOutcome    `json:"window_outcome"`
	Decision       QuotaDecision    `json:"decision,omitempty"`
	HTTPStatus     int              `json:"http_status,omitempty"`
	LatencyMS      int64            `json:"latency_ms"`
	ErrorCode      ErrorCode        `json:"error_code,omitempty"`
}

type OccurrenceStatus string

const (
	OccurrencePlanned   OccurrenceStatus = "planned"
	OccurrenceRunning   OccurrenceStatus = "running"
	OccurrenceSucceeded OccurrenceStatus = "succeeded"
	OccurrenceFailed    OccurrenceStatus = "failed"
	OccurrenceSkipped   OccurrenceStatus = "skipped"
	OccurrenceMissed    OccurrenceStatus = "missed"
)

type PlannedOccurrence struct {
	ID           string    `json:"id"`
	AccountKey   string    `json:"account_key"`
	LocalDate    string    `json:"local_date"`
	PeriodIndex  int       `json:"period_index"`
	WindowStart  time.Time `json:"window_start"`
	WindowEnd    time.Time `json:"window_end"`
	PlannedAt    time.Time `json:"planned_at"`
	Missed       bool      `json:"missed"`
	MissedReason string    `json:"missed_reason,omitempty"`
}

type OccurrenceState struct {
	PlannedOccurrence
	Status                OccurrenceStatus `json:"status"`
	CompensationDueAt     time.Time        `json:"compensation_due_at,omitempty"`
	CompensationAttempted bool             `json:"compensation_attempted"`
}

type RuntimeState struct {
	SchemaVersion  int                        `json:"schema_version"`
	Occurrences    map[string]OccurrenceState `json:"occurrences"`
	NextRuns       map[string]time.Time       `json:"next_runs"`
	GuardrailHolds map[string]GuardrailHold   `json:"guardrail_holds"`
}
