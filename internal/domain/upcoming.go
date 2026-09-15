package domain

import "time"

// BlockedReason names a condition that is already true and will make an
// upcoming occurrence skip its request. It is deliberately limited to durable
// state the plugin can assert right now; a quota decision depends on the
// snapshot at execution time and is never predicted here.
type BlockedReason string

const (
	BlockedGuardrailHold BlockedReason = "guardrail_hold"
)

// UpcomingOccurrence is one armed preheat slot. Identity is the account key
// only: the panel already holds the account list and applies the Operator's
// identity-masking preference to it, so repeating a masked identity here would
// cost an account discovery round trip and create a second masking path.
type UpcomingOccurrence struct {
	AccountKey    string        `json:"account_key"`
	PlannedAt     time.Time     `json:"planned_at"`
	BlockedReason BlockedReason `json:"blocked_reason,omitempty"`
}

// UpcomingBatch groups the staggered occurrences of one Preheat Window, which
// is the same grouping the history panel uses for executed batches.
type UpcomingBatch struct {
	LocalDate   string               `json:"local_date"`
	PeriodIndex int                  `json:"period_index"`
	WindowStart time.Time            `json:"window_start"`
	WindowEnd   time.Time            `json:"window_end"`
	Occurrences []UpcomingOccurrence `json:"occurrences"`
}

// UpcomingView answers "is automatic preheating still alive, and when does it
// act next". Enabled and StoreErrorCode travel with the batches so the panel
// can tell "nothing is scheduled" apart from "the schedule is off" and from
// "the state could not be read" without correlating two responses.
type UpcomingView struct {
	Enabled bool `json:"enabled"`
	// Superseded means another plugin instance owns scheduling, so the batches
	// below are armed in this process but will not run.
	Superseded     bool            `json:"superseded,omitempty"`
	StoreErrorCode ErrorCode       `json:"store_error_code,omitempty"`
	NextRunAt      time.Time       `json:"next_run_at,omitempty"`
	Batches        []UpcomingBatch `json:"batches"`
}
