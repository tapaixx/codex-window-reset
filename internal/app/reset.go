package app

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

var canonicalResetUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type resetFlight struct {
	accountKey string
	done       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	stopAfter  func() bool
	audit      domain.ResetAudit
	err        error
}

// ResetQuota owns the durable reset state machine. The pending append is the
// commit point before the irreversible upstream consume call; all later
// failures preserve that evidence and classify the caller-visible result.
func (r *Runtime) ResetQuota(ctx context.Context, accountKey, idempotencyKey string) (audit domain.ResetAudit, err error) {
	if r == nil {
		return domain.ResetAudit{}, context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	accountKey = normalizeKey(accountKey)
	if !canonicalResetUUID.MatchString(idempotencyKey) {
		return domain.ResetAudit{}, appError(domain.CodeConfigInvalid, 400, false, "idempotency key must be a canonical UUID")
	}
	if r.deps.Audit == nil {
		return domain.ResetAudit{}, appError(domain.CodeStoreCorrupt, 500, false, "reset audit repository is unavailable")
	}
	if accountKey == "" {
		return domain.ResetAudit{}, appError(domain.CodeAccountUnavailable, 404, false, "account was not discovered")
	}

	if flight := r.resetFlightFor(idempotencyKey); flight != nil {
		if flight.accountKey != accountKey {
			return domain.ResetAudit{}, idempotencyConflictError()
		}
		return r.waitResetFlight(ctx, idempotencyKey, flight)
	}

	existing, found, findErr := r.deps.Audit.FindByKey(idempotencyKey)
	if findErr != nil {
		r.setStoreError(findErr)
		return domain.ResetAudit{}, resetStoreError(findErr)
	}
	if found {
		if existing.AccountKey != accountKey {
			return domain.ResetAudit{}, idempotencyConflictError()
		}
		if existing.Outcome != domain.ResetPending {
			return existing, nil
		}
		// A pending record without a live in-process flight belongs to a
		// previous process (or a process that stopped before registration). Treat
		// it as crash ambiguity and never issue a second consume call.
		return r.recoverOrphanedReset(existing)
	}

	account, accountErr := r.deps.Accounts.Find(ctx, accountKey)
	if accountErr != nil {
		return domain.ResetAudit{}, appError(domain.CodeAccountUnavailable, 404, false, "account was not discovered")
	}
	account.Key = accountKey
	if account.Disabled {
		return domain.ResetAudit{}, appError(domain.CodeAccountDisabled, 409, false, "account is disabled")
	}
	if account.Unavailable {
		return domain.ResetAudit{}, appError(domain.CodeAccountUnavailable, 409, true, "account is unavailable")
	}

	flight, started := r.beginResetFlight(ctx, accountKey, idempotencyKey)
	if !started {
		if flight != nil {
			if flight.accountKey != accountKey {
				return domain.ResetAudit{}, idempotencyConflictError()
			}
			return r.waitResetFlight(ctx, idempotencyKey, flight)
		}
		return domain.ResetAudit{}, appError(domain.CodeAccountBusy, 409, true, "account is already in use")
	}
	defer func() {
		r.finishResetFlight(idempotencyKey, audit, err)
	}()

	refreshCtx, refreshCancel := context.WithTimeout(flight.ctx, quotaRefreshTimeout)
	snapshot, refreshErr := r.deps.Quota.Refresh(refreshCtx, account)
	refreshCancel()
	if refreshErr != nil {
		return domain.ResetAudit{}, appError(domain.CodeQuotaRefreshFailed, 502, true, "quota refresh failed")
	}
	if !snapshot.ResetInfoComplete || snapshot.ResetApplicableCount == nil || *snapshot.ResetApplicableCount <= 0 {
		return domain.ResetAudit{}, appError(domain.CodeQuotaRefreshFailed, 409, false, "no applicable reset credits are available")
	}

	requestedAt := r.now()
	priorCredits := *snapshot.ResetApplicableCount
	pending := domain.ResetAudit{
		IdempotencyKey:         idempotencyKey,
		RequestedAt:            requestedAt,
		AccountKey:             accountKey,
		MaskedIdentity:         safeMaskedIdentity(account.MaskedIdentity),
		PriorApplicableCredits: &priorCredits,
		Outcome:                domain.ResetPending,
		CorrelationID:          r.nextID(),
	}
	if appendErr := r.deps.Audit.AppendPending(pending); appendErr != nil {
		return r.handlePendingAppendConflict(accountKey, idempotencyKey, appendErr)
	}

	result, callErr, entered := r.resetConsume(flight, account, idempotencyKey)
	if !entered {
		audit = pending
		err = resetOutcomeUnknownError()
		return audit, err
	}
	// The refresh is deliberately unconditional after entering the upstream
	// consume primitive, including definite HTTP failures.
	postRefreshCtx, postRefreshCancel := context.WithTimeout(flight.ctx, quotaRefreshTimeout)
	_, _ = r.deps.Quota.Refresh(postRefreshCtx, account)
	postRefreshCancel()

	audit = pending
	audit.FinishedAt = r.now()
	audit.HTTPCategory = result.Category
	audit.Outcome = finalResetOutcome(result, callErr)
	if replaceErr := r.deps.Audit.Replace(audit); replaceErr != nil {
		r.setStoreError(replaceErr)
		audit.Outcome = domain.ResetUnknown
		err = resetOutcomeUnknownError()
		return audit, err
	}
	if audit.Outcome == domain.ResetUnknown {
		err = resetOutcomeUnknownError()
		return audit, err
	}
	return audit, nil
}

func finalResetOutcome(result domain.ResetHTTPResult, callErr error) domain.ResetOutcome {
	if callErr == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
		return domain.ResetSucceeded
	}
	if result.StatusCode == 0 || errors.Is(callErr, context.DeadlineExceeded) {
		return domain.ResetUnknown
	}
	return domain.ResetFailed
}

func (r *Runtime) resetFlightFor(idempotencyKey string) *resetFlight {
	r.mu.RLock()
	flight := r.resetFlights[idempotencyKey]
	r.mu.RUnlock()
	return flight
}

func (r *Runtime) beginResetFlight(ctx context.Context, accountKey, idempotencyKey string) (*resetFlight, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if flight := r.resetFlights[idempotencyKey]; flight != nil {
		return flight, false
	}
	if r.stopped {
		return nil, false
	}
	if _, busy := r.busy[accountKey]; busy {
		return nil, false
	}
	flightCtx, cancel := context.WithCancel(ctx)
	var stopAfter func() bool
	if r.stopCtx != nil {
		stopAfter = context.AfterFunc(r.stopCtx, cancel)
	}
	flight := &resetFlight{
		accountKey: accountKey,
		done:       make(chan struct{}),
		ctx:        flightCtx,
		cancel:     cancel,
		stopAfter:  stopAfter,
	}
	r.busy[accountKey] = struct{}{}
	if r.resetFlights == nil {
		r.resetFlights = make(map[string]*resetFlight)
	}
	r.resetFlights[idempotencyKey] = flight
	r.wg.Add(1)
	return flight, true
}

func (r *Runtime) finishResetFlight(idempotencyKey string, audit domain.ResetAudit, err error) {
	var cancel context.CancelFunc
	var stopAfter func() bool
	r.mu.Lock()
	flight := r.resetFlights[idempotencyKey]
	if flight == nil {
		r.mu.Unlock()
		return
	}
	flight.audit = audit
	flight.err = err
	cancel = flight.cancel
	stopAfter = flight.stopAfter
	delete(r.resetFlights, idempotencyKey)
	delete(r.busy, flight.accountKey)
	close(flight.done)
	r.mu.Unlock()
	if stopAfter != nil {
		stopAfter()
	}
	if cancel != nil {
		cancel()
	}
	r.wg.Done()
}

func (r *Runtime) waitResetFlight(ctx context.Context, idempotencyKey string, flight *resetFlight) (domain.ResetAudit, error) {
	select {
	case <-flight.done:
		if flight.err != nil && flight.audit.Outcome == domain.ResetPending {
			return flight.audit, flight.err
		}
		reloaded, found, err := r.deps.Audit.FindByKey(idempotencyKey)
		if err == nil && found && reloaded.Outcome != domain.ResetPending {
			return reloaded, flight.err
		}
		if err != nil {
			r.setStoreError(err)
			return flight.audit, resetStoreError(err)
		}
		return flight.audit, flight.err
	case <-ctx.Done():
		return domain.ResetAudit{}, resetWaitCanceledError()
	}
}

func (r *Runtime) resetConsume(flight *resetFlight, account accounts.Account, idempotencyKey string) (domain.ResetHTTPResult, error, bool) {
	r.resetGate.RLock()
	defer r.resetGate.RUnlock()
	r.mu.RLock()
	stopped := r.stopped
	r.mu.RUnlock()
	if stopped || flight.ctx.Err() != nil {
		return domain.ResetHTTPResult{}, resetOutcomeUnknownError(), false
	}
	result, err := r.deps.Quota.Reset(flight.ctx, account, idempotencyKey)
	return result, err, true
}

func (r *Runtime) handlePendingAppendConflict(accountKey, idempotencyKey string, appendErr error) (domain.ResetAudit, error) {
	if domain.CodeOf(appendErr) != domain.CodeIdempotencyConflict {
		r.setStoreError(appendErr)
		return domain.ResetAudit{}, resetStoreError(appendErr)
	}
	existing, found, findErr := r.deps.Audit.FindByKey(idempotencyKey)
	if findErr != nil {
		r.setStoreError(findErr)
		return domain.ResetAudit{}, resetStoreError(findErr)
	}
	if !found {
		return domain.ResetAudit{}, idempotencyConflictError()
	}
	if existing.AccountKey != accountKey {
		return domain.ResetAudit{}, idempotencyConflictError()
	}
	if existing.Outcome == domain.ResetPending {
		return domain.ResetAudit{}, appError(domain.CodeAccountBusy, 409, true, "account is already in use")
	}
	return existing, nil
}

func (r *Runtime) recoverOrphanedReset(record domain.ResetAudit) (domain.ResetAudit, error) {
	recovered := record
	recovered.Outcome = domain.ResetUnknown
	recovered.FinishedAt = r.now()
	if err := r.deps.Audit.Replace(recovered); err != nil {
		r.setStoreError(err)
		return record, resetOutcomeUnknownError()
	}
	return recovered, nil
}

func (r *Runtime) ListResetAudit() ([]domain.ResetAudit, error) {
	if r == nil {
		return nil, context.Canceled
	}
	if r.deps.Audit == nil {
		return nil, appError(domain.CodeStoreCorrupt, 500, false, "reset audit repository is unavailable")
	}
	records, err := r.deps.Audit.Load()
	if err != nil {
		r.setStoreError(err)
		return nil, resetStoreError(err)
	}
	return records, nil
}

func (r *Runtime) ClearResetAudit(phrase string) error {
	if phrase != "DELETE AUDIT" {
		return appError(domain.CodeConfigInvalid, 400, false, "exact confirmation phrase is required")
	}
	if r == nil {
		return context.Canceled
	}
	if r.deps.Audit == nil {
		return appError(domain.CodeStoreCorrupt, 500, false, "reset audit repository is unavailable")
	}
	if err := r.deps.Audit.Clear(); err != nil {
		r.setStoreError(err)
		return resetStoreError(err)
	}
	r.clearStoreError()
	return nil
}

func safeMaskedIdentity(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return "***"
	}
	return identity
}

func idempotencyConflictError() error {
	return appError(domain.CodeIdempotencyConflict, 409, false, "idempotency key belongs to another account")
}

func resetOutcomeUnknownError() error {
	return appError(domain.CodeResetOutcomeUnknown, 503, true, "reset outcome is unknown")
}

func resetWaitCanceledError() error {
	return appError(domain.CodeAccountBusy, 409, true, "reset is already in use")
}

func resetStoreError(err error) error {
	if domain.CodeOf(err) == domain.CodeIdempotencyConflict {
		return idempotencyConflictError()
	}
	return appError(domain.CodeStoreCorrupt, 500, true, "reset audit persistence failed")
}
