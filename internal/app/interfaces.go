// Package app contains the application-level orchestration for account
// probes, quota refreshes, and scheduled preheats.  It is deliberately
// expressed in terms of small interfaces so the host boundary and the
// persistent repositories can be replaced by deterministic test doubles.
package app

import (
	"context"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// ConfigRepository is the configuration persistence boundary used by Runtime.
type ConfigRepository interface {
	Load() (domain.Config, error)
	Save(domain.Config) error
}

// HistoryRepository stores operation records.  Runtime appends records one at
// a time so a failed operation cannot disappear from the audit trail merely
// because a sibling operation in a bulk run failed.
type HistoryRepository interface {
	Load() ([]domain.OperationRecord, error)
	Append(domain.OperationRecord) error
	Clear() error
}

// RuntimeStateRepository is the transactional boundary shared by quota
// guardrail holds, occurrence state, and compensation eligibility.
type RuntimeStateRepository interface {
	Load() (domain.RuntimeState, error)
	Save(domain.RuntimeState) error
	Update(func(*domain.RuntimeState) error) error
}

// ResetAuditRepository is included here because Runtime owns this complete
// repository graph.  Quota reset behavior is added by a later application
// task; keeping the dependency boundary stable lets that task reuse Runtime.
type ResetAuditRepository interface {
	Load() ([]domain.ResetAudit, error)
	FindByKey(string) (domain.ResetAudit, bool, error)
	AppendPending(domain.ResetAudit) error
	Replace(domain.ResetAudit) error
	DeleteBefore(time.Time) error
	RecoverPending(time.Time) error
	Clear() error
}

// ProbeExecutor performs one sanitized upstream model request.
type ProbeExecutor interface {
	Execute(context.Context, accounts.Account, string, time.Duration) domain.ProbeResult
}

// QuotaService owns one-account refresh single-flight and quota decisions.  A
// bulk maximum-three worker pool belongs to Runtime, not this interface.
type QuotaService interface {
	Refresh(context.Context, accounts.Account) (domain.UsageSnapshot, error)
	Get(string, time.Time) (domain.SnapshotView, bool)
	Reset(context.Context, accounts.Account, string) (domain.ResetHTTPResult, error)
}

// quotaEvaluator is optional for test doubles and preserves the public
// QuotaService contract from the earlier quota task.  The real quota.Service
// implements it and owns persistent guardrail transitions.
type quotaEvaluator interface {
	Evaluate(domain.Config, string, time.Time) domain.QuotaDecision
}

// AccountService discovers current host account state.  Runtime re-reads it
// at each operation boundary so Disabled and Unavailable state is not stale.
type AccountService interface {
	List(context.Context) ([]accounts.Account, error)
	Find(context.Context, string) (accounts.Account, error)
}

// Planner is kept in Dependencies for callers that want to share the exact
// planner used by scheduling.  Task 7 does not plan occurrences itself, but
// keeping the seam here avoids coupling orchestration to a concrete planner.
type Planner interface {
	PlanDay(domain.Config, time.Time) ([]domain.PlannedOccurrence, error)
}
