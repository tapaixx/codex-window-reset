package app

import (
	"context"
	"sync"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// StartManualProbes starts one asynchronous bulk Health Probe run.  The
// allowUnavailable flag is the explicit Operator override for host-reported
// Unavailable accounts; Disabled accounts are never overridable.
func (r *Runtime) StartManualProbes(ctx context.Context, keys []string, allowUnavailable bool) (string, error) {
	if r == nil {
		return "", context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Reject an overlapping bulk run before any host I/O.  This gives callers a
	// stable run_in_progress response even when account discovery is slow.
	r.mu.RLock()
	stopped := r.stopped
	inProgress := r.run != nil && r.run.active
	r.mu.RUnlock()
	if stopped {
		return "", appError(domain.CodeConfigInvalid, 409, false, "runtime is stopped")
	}
	if inProgress {
		return "", appError(domain.CodeRunInProgress, 409, true, "a manual probe run is already in progress")
	}

	normalized, err := requestedKeys(keys)
	if err != nil {
		return "", err
	}
	discovered, err := r.deps.Accounts.List(ctx)
	if err != nil {
		return "", err
	}
	byKey := make(map[string]accounts.Account, len(discovered))
	for _, account := range discovered {
		key := normalizeKey(account.Key)
		if key == "" {
			continue
		}
		if _, exists := byKey[key]; !exists {
			account.Key = key
			byKey[key] = account
		}
	}
	selected := make([]accounts.Account, 0, len(normalized))
	for _, key := range normalized {
		account, exists := byKey[key]
		if !exists {
			return "", appError(domain.CodeAccountUnavailable, 404, false, "account was not discovered")
		}
		if account.Disabled {
			return "", appError(domain.CodeAccountDisabled, 409, false, "account is disabled")
		}
		if account.Unavailable && !allowUnavailable {
			return "", appError(domain.CodeAccountUnavailable, 409, true, "account is unavailable")
		}
		if r.isBusy(key) {
			return "", appError(domain.CodeAccountBusy, 409, true, "account is already in use")
		}
		selected = append(selected, account)
	}

	// Discovery and eligibility checks happen outside Runtime.mu.  The final
	// check-and-install closes the race between two callers that both passed
	// the initial read above.
	runID := r.nextID()
	runCtx, cancel := context.WithCancel(ctx)
	run := &runState{
		id:     runID,
		total:  len(selected),
		active: true,
		done:   make(chan struct{}),
		cancel: cancel,
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		cancel()
		return "", appError(domain.CodeConfigInvalid, 409, false, "runtime is stopped")
	}
	if r.run != nil && r.run.active {
		r.mu.Unlock()
		cancel()
		return "", appError(domain.CodeRunInProgress, 409, true, "a manual probe run is already in progress")
	}
	r.run = run
	r.wg.Add(1)
	r.mu.Unlock()

	go r.runManual(runCtx, run, selected)
	return runID, nil
}

func requestedKeys(keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, appError(domain.CodeConfigInvalid, 400, false, "at least one account key is required")
	}
	seen := make(map[string]struct{}, len(keys))
	result := make([]string, 0, len(keys))
	for _, raw := range keys {
		key := normalizeKey(raw)
		if key == "" {
			return nil, appError(domain.CodeConfigInvalid, 400, false, "account keys must not be empty")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, appError(domain.CodeConfigInvalid, 400, false, "account keys must be unique")
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result, nil
}

// RefreshQuotas refreshes each requested account concurrently with the same
// three-worker ceiling used by manual probes.  A per-account refresh failure
// is represented in that account's SnapshotView; it does not discard sibling
// results or turn an otherwise useful bulk response into an all-or-nothing
// error.
func (r *Runtime) RefreshQuotas(ctx context.Context, keys []string) ([]domain.SnapshotView, error) {
	if r == nil {
		return nil, context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := requestedKeys(keys)
	if err != nil {
		return nil, err
	}
	views := make([]domain.SnapshotView, len(normalized))
	jobs := make(chan int)
	workers := len(normalized)
	if workers > 3 {
		workers = 3
	}
	var workerWG sync.WaitGroup
	workerWG.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer workerWG.Done()
			for index := range jobs {
				views[index] = r.refreshQuotaOne(ctx, normalized[index])
			}
		}()
	}
	for index := range normalized {
		select {
		case jobs <- index:
		case <-ctx.Done():
			// Produce deterministic error views for work that could not be
			// dispatched.  Already-running workers are allowed to finish.
			for remaining := index; remaining < len(normalized); remaining++ {
				views[remaining] = failedSnapshotView(normalized[remaining], ctx.Err(), r.now())
			}
			close(jobs)
			workerWG.Wait()
			return views, nil
		}
	}
	close(jobs)
	workerWG.Wait()
	return views, nil
}

func (r *Runtime) refreshQuotaOne(ctx context.Context, key string) domain.SnapshotView {
	account, err := r.deps.Accounts.Find(ctx, key)
	if err != nil {
		return failedSnapshotView(key, err, r.now())
	}
	account.Key = key
	snapshot, refreshErr := r.refreshSnapshot(ctx, account)
	now := r.now()
	if refreshErr != nil {
		view, ok := r.deps.Quota.Get(key, now)
		if !ok {
			return failedSnapshotView(key, refreshErr, now)
		}
		if view.Snapshot.AccountKey == "" {
			view.Snapshot.AccountKey = key
		}
		view.RefreshErrorCode = domain.CodeQuotaRefreshFailed
		if view.LastAttemptAt.IsZero() {
			view.LastAttemptAt = now.UTC()
		}
		return view
	}

	if view, ok := r.deps.Quota.Get(key, now); ok {
		// A real quota service updates Get atomically as part of Refresh.  If a
		// lightweight implementation does not, prefer the successful value
		// returned by Refresh so callers never receive an older snapshot after a
		// successful request.
		view.Snapshot = *snapshot
		view.RefreshErrorCode = ""
		if view.Snapshot.AccountKey == "" {
			view.Snapshot.AccountKey = key
		}
		if view.LastAttemptAt.IsZero() {
			view.LastAttemptAt = now.UTC()
		}
		return view
	}
	return domain.SnapshotView{Snapshot: *snapshot, LastAttemptAt: now.UTC()}
}

func failedSnapshotView(key string, err error, now time.Time) domain.SnapshotView {
	_ = err
	return domain.SnapshotView{
		Snapshot:         domain.UsageSnapshot{AccountKey: normalizeKey(key)},
		LastAttemptAt:    now.UTC(),
		RefreshErrorCode: domain.CodeQuotaRefreshFailed,
	}
}

func (r *Runtime) runManual(ctx context.Context, run *runState, selected []accounts.Account) {
	defer r.wg.Done()
	workers := len(selected)
	if workers > 3 {
		workers = 3
	}
	if workers == 0 {
		r.finishRun(run)
		return
	}

	jobs := make(chan accounts.Account)
	var workerWG sync.WaitGroup
	workerWG.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer workerWG.Done()
			for account := range jobs {
				r.executeManual(ctx, account)
				r.completeOne(run)
			}
		}()
	}

	dispatched := 0
dispatch:
	for dispatched < len(selected) {
		account := selected[dispatched]
		select {
		case jobs <- account:
			dispatched++
		case <-ctx.Done():
			// Keep progress accounting total-consistent when Stop or the
			// caller cancels before all jobs are dispatched.
			for remaining := dispatched; remaining < len(selected); remaining++ {
				r.completeOne(run)
			}
			break dispatch
		}
	}
	close(jobs)
	workerWG.Wait()
	r.finishRun(run)
}

func (r *Runtime) completeOne(run *runState) {
	r.mu.Lock()
	if run != nil {
		run.completed++
	}
	r.mu.Unlock()
}

func (r *Runtime) finishRun(run *runState) {
	r.mu.Lock()
	if run != nil {
		run.active = false
		if run.cancel != nil {
			run.cancel()
		}
		close(run.done)
	}
	r.mu.Unlock()
}

func (r *Runtime) executeManual(ctx context.Context, account accounts.Account) {
	if !r.acquireBusy(account.Key) {
		record := operationBase(r, domain.TriggerHealthProbe, "", account)
		record.RequestOutcome = domain.RequestResponseError
		record.WindowOutcome = domain.WindowNotObserved
		record.ErrorCode = domain.CodeAccountBusy
		record.FinishedAt = r.now()
		r.appendHistory(record)
		return
	}
	defer r.releaseBusy(account.Key)

	started := r.now()
	before, _ := r.refreshSnapshot(ctx, account)
	config := r.configSnapshot()
	result := r.deps.Probe.Execute(ctx, account, config.ProbeModel, probeTimeout(config))
	if result.Outcome == "" {
		result.Outcome = domain.RequestResponseError
		if result.ErrorCode == "" {
			result.ErrorCode = domain.CodeProbeFailed
		}
	}
	after, _ := r.refreshSnapshot(ctx, account)

	record := operationBaseAt(r, domain.TriggerHealthProbe, "", account, started)
	record.RequestOutcome = result.Outcome
	record.WindowOutcome = classifyWindow(result.Outcome, before, after)
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
	r.appendHistory(record)
}

func probeTimeout(config domain.Config) time.Duration {
	seconds := config.ProbeTimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func (r *Runtime) refreshSnapshot(ctx context.Context, account accounts.Account) (*domain.UsageSnapshot, error) {
	snapshot, err := r.deps.Quota.Refresh(ctx, account)
	if err != nil {
		return nil, err
	}
	snapshot.AccountKey = normalizeKey(account.Key)
	snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	return &snapshot, nil
}

func (r *Runtime) appendHistory(record domain.OperationRecord) {
	if r == nil || r.deps.History == nil {
		return
	}
	if err := r.deps.History.Append(record); err != nil {
		r.setStoreError(err)
	}
}
