package domain

import (
	"encoding/json"
	"time"
)

type OperationTrigger string

const (
	TriggerHealthProbe  OperationTrigger = "health_probe"
	TriggerPreheat      OperationTrigger = "preheat"
	TriggerCompensation OperationTrigger = "compensation"
)

type OperationRecord struct {
	ID             string           `json:"id"`
	RunID          string           `json:"run_id,omitempty"`
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

func (r OperationRecord) MarshalJSON() ([]byte, error) {
	type wire OperationRecord
	value := wire(r)
	value.StartedAt = r.StartedAt.UTC()
	value.FinishedAt = r.FinishedAt.UTC()
	return json.Marshal(value)
}

func (r *OperationRecord) UnmarshalJSON(data []byte) error {
	type wire OperationRecord
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*r = OperationRecord(value)
	r.StartedAt = r.StartedAt.UTC()
	r.FinishedAt = r.FinishedAt.UTC()
	return nil
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

func (o PlannedOccurrence) MarshalJSON() ([]byte, error) {
	type wire PlannedOccurrence
	value := wire(o)
	value.WindowStart = o.WindowStart.UTC()
	value.WindowEnd = o.WindowEnd.UTC()
	value.PlannedAt = o.PlannedAt.UTC()
	return json.Marshal(value)
}

func (o *PlannedOccurrence) UnmarshalJSON(data []byte) error {
	type wire PlannedOccurrence
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = PlannedOccurrence(value)
	o.WindowStart = o.WindowStart.UTC()
	o.WindowEnd = o.WindowEnd.UTC()
	o.PlannedAt = o.PlannedAt.UTC()
	return nil
}

type OccurrenceState struct {
	PlannedOccurrence
	Status                OccurrenceStatus `json:"status"`
	CompensationDueAt     time.Time        `json:"compensation_due_at,omitempty"`
	CompensationAttempted bool             `json:"compensation_attempted"`
}

func (s OccurrenceState) MarshalJSON() ([]byte, error) {
	return json.Marshal(occurrenceStateWire{
		ID:                    s.ID,
		AccountKey:            s.AccountKey,
		LocalDate:             s.LocalDate,
		PeriodIndex:           s.PeriodIndex,
		WindowStart:           s.WindowStart.UTC(),
		WindowEnd:             s.WindowEnd.UTC(),
		PlannedAt:             s.PlannedAt.UTC(),
		Missed:                s.Missed,
		MissedReason:          s.MissedReason,
		Status:                s.Status,
		CompensationDueAt:     s.CompensationDueAt.UTC(),
		CompensationAttempted: s.CompensationAttempted,
	})
}

func (s *OccurrenceState) UnmarshalJSON(data []byte) error {
	var value occurrenceStateWire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	s.PlannedOccurrence = PlannedOccurrence{
		ID:           value.ID,
		AccountKey:   value.AccountKey,
		LocalDate:    value.LocalDate,
		PeriodIndex:  value.PeriodIndex,
		WindowStart:  value.WindowStart.UTC(),
		WindowEnd:    value.WindowEnd.UTC(),
		PlannedAt:    value.PlannedAt.UTC(),
		Missed:       value.Missed,
		MissedReason: value.MissedReason,
	}
	s.Status = value.Status
	s.CompensationDueAt = value.CompensationDueAt.UTC()
	s.CompensationAttempted = value.CompensationAttempted
	return nil
}

type occurrenceStateWire struct {
	ID                    string           `json:"id"`
	AccountKey            string           `json:"account_key"`
	LocalDate             string           `json:"local_date"`
	PeriodIndex           int              `json:"period_index"`
	WindowStart           time.Time        `json:"window_start"`
	WindowEnd             time.Time        `json:"window_end"`
	PlannedAt             time.Time        `json:"planned_at"`
	Missed                bool             `json:"missed"`
	MissedReason          string           `json:"missed_reason,omitempty"`
	Status                OccurrenceStatus `json:"status"`
	CompensationDueAt     time.Time        `json:"compensation_due_at,omitempty"`
	CompensationAttempted bool             `json:"compensation_attempted"`
}

type RuntimeState struct {
	SchemaVersion int                        `json:"schema_version"`
	Occurrences   map[string]OccurrenceState `json:"occurrences"`
	NextRuns      map[string]time.Time       `json:"next_runs"`
	// SchedulerOwner names the single plugin instance allowed to execute
	// occurrences. A host that loads a new plugin build without unloading the
	// previous one leaves two schedulers alive over one data directory, each
	// with its own in-process lock, so the occurrence claim is not actually
	// atomic between them and the same slot can run twice. The owner is
	// written once at startup and only read afterwards, so every instance
	// agrees on it without needing a shared lock.
	SchedulerOwner string                   `json:"scheduler_owner,omitempty"`
	GuardrailHolds map[string]GuardrailHold `json:"guardrail_holds"`
}

func (s RuntimeState) MarshalJSON() ([]byte, error) {
	type wire RuntimeState
	value := wire(s)
	value.NextRuns = utcTimes(s.NextRuns)
	return json.Marshal(value)
}

func (s *RuntimeState) UnmarshalJSON(data []byte) error {
	type wire RuntimeState
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = RuntimeState(value)
	s.NextRuns = utcTimes(s.NextRuns)
	return nil
}
