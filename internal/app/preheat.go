package app

import (
	"context"
	"strings"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

const fallbackSnapshotStaleAfter = 5 * time.Minute

// ExecutePreheat performs one scheduler-owned preheat operation.  The
// scheduler-facing API intentionally returns a record rather than an error:
// every attempted or skipped occurrence has one durable operation record,
// including a sanitized reason for an eligibility skip.
func (r *Runtime) ExecutePreheat(ctx context.Context, occurrence domain.PlannedOccurrence) domain.OperationRecord {
	if ctx == nil {
		ctx = context.Background()
	}
	key := normalizeKey(occurrence.AccountKey)
	occurrence.AccountKey = key
	started := r.now()

	account, findErr := r.deps.Accounts.Find(ctx, key)
	if findErr != nil {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.AccountKey = key
		record.RequestOutcome = domain.RequestDisabled
		record.WindowOutcome = domain.WindowNotObserved
		record.ErrorCode = domain.CodeAccountUnavailable
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}
	account.Key = key

	if occurrence.Missed {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.RequestOutcome = domain.RequestDisabled
		record.WindowOutcome = domain.WindowNotObserved
		record.ErrorCode = domain.CodeConfigInvalid
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}

	config := r.configSnapshot()
	if !config.Enabled || !isScheduled(config, key) {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.RequestOutcome = domain.RequestDisabled
		record.WindowOutcome = domain.WindowNotObserved
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}
	if account.Disabled {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.RequestOutcome = domain.RequestDisabled
		record.WindowOutcome = domain.WindowNotObserved
		record.ErrorCode = domain.CodeAccountDisabled
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}
	if account.Unavailable {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.RequestOutcome = domain.RequestDisabled
		record.WindowOutcome = domain.WindowNotObserved
		record.ErrorCode = domain.CodeAccountUnavailable
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}
	if !r.acquireBusy(key) {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.RequestOutcome = domain.RequestResponseError
		record.WindowOutcome = domain.WindowNotObserved
		record.ErrorCode = domain.CodeAccountBusy
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}
	defer r.releaseBusy(key)

	before, refreshErr := r.refreshSnapshot(ctx, account)
	now := r.now()
	decision := r.evaluateQuota(config, key, now, before, refreshErr)
	if decision == domain.DecisionGuardrailHold || decision == domain.DecisionSufficientWindow {
		record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
		record.RequestOutcome = domain.RequestDisabled
		record.WindowOutcome = domain.WindowNotObserved
		record.Decision = decision
		if decision == domain.DecisionGuardrailHold {
			record.ErrorCode = domain.CodeGuardrailHold
		}
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return record
	}
	if decision == "" {
		decision = domain.DecisionProceed
	}

	result := r.deps.Probe.Execute(ctx, account, config.ProbeModel, probeTimeout(config))
	if result.Outcome == "" {
		result.Outcome = domain.RequestResponseError
		if result.ErrorCode == "" {
			result.ErrorCode = domain.CodeProbeFailed
		}
	}
	after, _ := r.refreshSnapshot(ctx, account)

	record := operationBaseAt(r, domain.TriggerPreheat, occurrence.ID, account, started)
	record.RequestOutcome = result.Outcome
	record.WindowOutcome = classifyWindow(result.Outcome, before, after)
	record.Decision = decision
	record.HTTPStatus = result.HTTPStatus
	record.LatencyMS = result.LatencyMS
	record.ErrorCode = result.ErrorCode
	if record.RequestOutcome != domain.RequestSucceeded && record.ErrorCode == "" {
		record.ErrorCode = domain.CodeProbeFailed
	}
	if record.RequestOutcome == domain.RequestSucceeded && record.WindowOutcome == domain.WindowUnverified {
		record.ErrorCode = domain.CodeWindowUnverified
	}
	record.FinishedAt = r.now()

	// Compensation is represented durably in occurrence state.  Scheduler
	// Task 8 owns the timer and one-shot execution; Task 7 owns the exact
	// eligibility decision and due timestamp.
	due := time.Time{}
	if compensable(result) {
		due = r.now().Add(5 * time.Minute)
	}
	r.updateCompensation(occurrence, due)
	r.appendHistory(record)
	return record
}

func isScheduled(config domain.Config, key string) bool {
	key = normalizeKey(key)
	for _, selected := range config.ScheduledAccountKeys {
		if normalizeKey(selected) == key {
			return true
		}
	}
	return false
}

func (r *Runtime) evaluateQuota(config domain.Config, key string, now time.Time, before *domain.UsageSnapshot, refreshErr error) domain.QuotaDecision {
	if evaluator, ok := r.deps.Quota.(quotaEvaluator); ok {
		decision := evaluator.Evaluate(config, key, now)
		if decision != "" {
			return decision
		}
	}
	return fallbackDecision(r, config, key, now, before, refreshErr)
}

// fallbackDecision keeps app fakes useful without weakening the production
// quota contract: the real service implements Evaluate and performs the
// transactional hold transition.  This fallback only applies when a small
// test or embedding implementation exposes Refresh/Get but not Evaluate.
func fallbackDecision(r *Runtime, config domain.Config, key string, now time.Time, refreshed *domain.UsageSnapshot, refreshErr error) domain.QuotaDecision {
	var snapshot *domain.UsageSnapshot
	var fresh bool
	if refreshed != nil {
		snapshot = refreshed
		fresh = snapshotIsFresh(refreshed, now)
	} else if view, ok := r.deps.Quota.Get(key, now); ok && view.RefreshErrorCode == "" && !view.Stale && snapshotIsFresh(&view.Snapshot, now) {
		copy := view.Snapshot
		snapshot = &copy
		fresh = true
	}
	if refreshErr != nil || snapshot == nil {
		if r.hasHold(key) {
			return domain.DecisionGuardrailHold
		}
		return domain.DecisionUnknownFailOpen
	}
	shortIndex, ok := shortestWindowIndex(snapshot.Windows)
	if !ok {
		return domain.DecisionProceed
	}
	for index, window := range snapshot.Windows {
		if index != shortIndex && window.RemainingPercent <= config.LongWindowFloorPercent {
			return domain.DecisionGuardrailHold
		}
	}
	short := snapshot.Windows[shortIndex]
	if fresh && short.RemainingPercent >= config.RemainingQuotaFloorPercent &&
		!short.ResetAt.IsZero() &&
		short.ResetAt.Sub(now.UTC()) >= time.Duration(config.RemainingWindowFloorMinutes)*time.Minute {
		return domain.DecisionSufficientWindow
	}
	return domain.DecisionProceed
}

func snapshotIsFresh(snapshot *domain.UsageSnapshot, now time.Time) bool {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return false
	}
	return now.UTC().Sub(snapshot.CapturedAt.UTC()) < fallbackSnapshotStaleAfter
}

func shortestWindowIndex(windows []domain.UsageWindow) (int, bool) {
	if len(windows) == 0 {
		return 0, false
	}
	marked := -1
	for index, window := range windows {
		if !window.Short {
			continue
		}
		if marked == -1 || window.DurationMinutes < windows[marked].DurationMinutes {
			marked = index
		}
	}
	if marked >= 0 {
		return marked, true
	}
	index := 0
	for candidate := 1; candidate < len(windows); candidate++ {
		if windows[candidate].DurationMinutes < windows[index].DurationMinutes {
			index = candidate
		}
	}
	return index, true
}

func (r *Runtime) hasHold(key string) bool {
	state, err := r.deps.State.Load()
	if err != nil {
		r.setStoreError(err)
		return true
	}
	_, ok := state.GuardrailHolds[normalizeKey(key)]
	return ok
}

func (r *Runtime) updateCompensation(occurrence domain.PlannedOccurrence, due time.Time) {
	if r == nil || r.deps.State == nil || strings.TrimSpace(occurrence.ID) == "" {
		return
	}
	if err := r.deps.State.Update(func(state *domain.RuntimeState) error {
		if state.Occurrences == nil {
			state.Occurrences = make(map[string]domain.OccurrenceState)
		}
		entry, exists := state.Occurrences[occurrence.ID]
		if !exists {
			entry = domain.OccurrenceState{PlannedOccurrence: occurrence}
		}
		if due.IsZero() {
			entry.CompensationDueAt = time.Time{}
		} else {
			entry.CompensationDueAt = due.UTC()
		}
		state.Occurrences[occurrence.ID] = entry
		return nil
	}); err != nil {
		r.setStoreError(err)
	}
}

func compensable(result domain.ProbeResult) bool {
	if result.HTTPStatus == 429 {
		return true
	}
	switch result.Outcome {
	case domain.RequestNetworkError, domain.RequestTimeout, domain.RequestRateLimited:
		return true
	case domain.RequestUpstreamError:
		return result.HTTPStatus >= 500 && result.HTTPStatus <= 599
	default:
		return false
	}
}

// classifyWindow intentionally takes request and window observations as
// separate inputs.  A failed request never becomes a successful window
// observation, and a successful request with missing/ambiguous quota data is
// explicitly unverified rather than fabricated as a reset.
func classifyWindow(request domain.RequestOutcome, before, after *domain.UsageSnapshot) domain.WindowOutcome {
	if request != domain.RequestSucceeded {
		return domain.WindowNotObserved
	}
	beforeWindow, beforeOK := shortestWindow(before)
	afterWindow, afterOK := shortestWindow(after)
	if !beforeOK || !afterOK || beforeWindow.ResetAt.IsZero() || afterWindow.ResetAt.IsZero() {
		return domain.WindowUnverified
	}
	if shortActive(before, beforeWindow) {
		return domain.WindowAlreadyActive
	}
	if !beforeWindow.ResetAt.Equal(afterWindow.ResetAt) {
		return domain.WindowVerifiedStarted
	}
	return domain.WindowUnchanged
}

func shortestWindow(snapshot *domain.UsageSnapshot) (domain.UsageWindow, bool) {
	if snapshot == nil {
		return domain.UsageWindow{}, false
	}
	index, ok := shortestWindowIndex(snapshot.Windows)
	if !ok {
		return domain.UsageWindow{}, false
	}
	return snapshot.Windows[index], true
}

func shortActive(snapshot *domain.UsageSnapshot, window domain.UsageWindow) bool {
	if snapshot == nil || window.ResetAt.IsZero() {
		return false
	}
	if !snapshot.CapturedAt.IsZero() {
		return window.ResetAt.After(snapshot.CapturedAt.UTC())
	}
	// Lightweight fixtures often omit CapturedAt.  A non-zero remaining
	// percentage is the only safe positive signal available in that shape.
	return window.RemainingPercent > 0
}

func operationBase(r *Runtime, trigger domain.OperationTrigger, occurrenceID string, account accounts.Account) domain.OperationRecord {
	return operationBaseAt(r, trigger, occurrenceID, account, r.now())
}

func operationBaseAt(r *Runtime, trigger domain.OperationTrigger, occurrenceID string, account accounts.Account, started time.Time) domain.OperationRecord {
	id := r.nextID()
	correlation := r.nextID()
	return domain.OperationRecord{
		ID:             id,
		CorrelationID:  correlation,
		Trigger:        trigger,
		OccurrenceID:   occurrenceID,
		AccountKey:     normalizeKey(account.Key),
		MaskedIdentity: account.MaskedIdentity,
		StartedAt:      started.UTC(),
		FinishedAt:     started.UTC(),
	}
}
