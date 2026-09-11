package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/schedule"
	"github.com/tapaixx/codex-window-reset/internal/simulate"
)

// Dependencies are the host, persistence, and pure-domain seams Runtime
// needs.  Concrete services from the other internal packages satisfy these
// interfaces directly; tests can provide small in-memory implementations.
type Dependencies struct {
	Accounts AccountService
	Probe    ProbeExecutor
	Quota    QuotaService
	Config   ConfigRepository
	History  HistoryRepository
	State    RuntimeStateRepository
	Audit    ResetAuditRepository
	Planner  Planner
	Clock    domain.Clock
	IDs      func() string
}

// Runtime is the sole owner of mutable application orchestration state.  In
// particular, the busy registry and bulk run state are never package globals.
type Runtime struct {
	mu           sync.RWMutex
	configMu     sync.Mutex
	deps         Dependencies
	config       domain.Config
	run          *runState
	busy         map[string]struct{}
	resetFlights map[string]*resetFlight
	stop         chan struct{}
	stopCtx      context.Context
	stopCancel   context.CancelFunc
	resetGate    sync.RWMutex
	wg           sync.WaitGroup
	stopOnce     sync.Once
	started      bool
	stopped      bool
	scheduler    *schedule.Scheduler

	// storeErrorCode is intentionally only a code.  Repository errors may
	// contain filesystem details and must not cross the management boundary.
	storeErrorCode domain.ErrorCode
}

type runState struct {
	id        string
	total     int
	completed int
	active    bool
	done      chan struct{}
	cancel    context.CancelFunc
}

// realClock is used only when an embedding host does not supply a clock.
// Production hosts and tests normally inject one so all orchestration times
// are controlled at the boundary.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

func (realClock) AfterFunc(delay time.Duration, fn func()) domain.Timer {
	return realTimer{timer: time.AfterFunc(delay, fn)}
}

type realTimer struct{ timer *time.Timer }

func (t realTimer) Stop() bool {
	if t.timer == nil {
		return false
	}
	return t.timer.Stop()
}

// New constructs a Runtime and loads its configuration.  A corrupt config is
// kept visible through Status while the runtime remains inert, allowing a
// management caller to inspect diagnostics and repair the file.  Other load
// failures are returned to the caller because there is no safe configuration
// to operate with.
func New(deps Dependencies) (*Runtime, error) {
	if deps.Accounts == nil {
		return nil, errors.New("account service is required")
	}
	if deps.Probe == nil {
		return nil, errors.New("probe executor is required")
	}
	if deps.Quota == nil {
		return nil, errors.New("quota service is required")
	}
	if deps.Config == nil {
		return nil, errors.New("config repository is required")
	}
	if deps.History == nil {
		return nil, errors.New("history repository is required")
	}
	if deps.State == nil {
		return nil, errors.New("runtime state repository is required")
	}
	if deps.Clock == nil {
		deps.Clock = realClock{}
	}
	if deps.IDs == nil {
		deps.IDs = defaultID
	}

	config, err := deps.Config.Load()
	stopCtx, stopCancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		deps:         deps,
		config:       cloneConfig(config),
		busy:         make(map[string]struct{}),
		resetFlights: make(map[string]*resetFlight),
		stop:         make(chan struct{}),
		stopCtx:      stopCtx,
		stopCancel:   stopCancel,
	}
	planner := deps.Planner
	if planner == nil {
		planner = schedule.Planner{}
	}
	// Construct the scheduler even when config loading reports corruption.
	// Runtime remains disabled for manual diagnostics, but Start must still
	// recover ownership of persisted work before any repair is attempted.
	runtime.scheduler = schedule.NewScheduler(deps.Clock, planner, deps.State, runtime)
	if err != nil {
		if domain.CodeOf(err) == domain.CodeStoreCorrupt {
			runtime.config = domain.DefaultConfig()
			runtime.storeErrorCode = domain.CodeStoreCorrupt
			return runtime, nil
		}
		return nil, err
	}
	return runtime, nil
}

func defaultID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	// The random source is expected on supported hosts.  This fallback keeps
	// the API usable in a constrained test/container environment without
	// introducing another mutable global sequence.
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
}

func (r *Runtime) nextID() string {
	if r != nil && r.deps.IDs != nil {
		if id := strings.TrimSpace(r.deps.IDs()); id != "" {
			return id
		}
	}
	return defaultID()
}

func (r *Runtime) now() time.Time {
	if r != nil && r.deps.Clock != nil {
		return r.deps.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runtime) configSnapshot() domain.Config {
	if r == nil {
		return domain.Config{}
	}
	r.mu.RLock()
	config := cloneConfig(r.config)
	r.mu.RUnlock()
	return config
}

func cloneConfig(config domain.Config) domain.Config {
	config.Weekdays = append([]int(nil), config.Weekdays...)
	config.WorkPeriods = append([]domain.LocalPeriod(nil), config.WorkPeriods...)
	config.BlackoutPeriods = append([]domain.LocalPeriod(nil), config.BlackoutPeriods...)
	config.ScheduledAccountKeys = append([]string(nil), config.ScheduledAccountKeys...)
	if config.PreheatLeadMinutes != nil {
		value := *config.PreheatLeadMinutes
		config.PreheatLeadMinutes = &value
	}
	if config.PreheatSpanMinutes != nil {
		value := *config.PreheatSpanMinutes
		config.PreheatSpanMinutes = &value
	}
	return config
}

func appError(code domain.ErrorCode, status int, retryable bool, message string) error {
	return &domain.Error{Code: code, HTTPStatus: status, Retryable: retryable, Message: message}
}

func (r *Runtime) setStoreError(err error) {
	if r == nil || err == nil {
		return
	}
	code := domain.CodeOf(err)
	if code == "" {
		code = domain.CodeStoreCorrupt
	}
	r.mu.Lock()
	r.storeErrorCode = code
	r.mu.Unlock()
}

func (r *Runtime) clearStoreError() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.storeErrorCode = ""
	r.mu.Unlock()
}

// ReportSchedulerError is consumed through schedule's private observer seam
// so asynchronous timer and reconciliation failures remain visible in the
// same sanitized status field as synchronous repository failures.
func (r *Runtime) ReportSchedulerError(err error) {
	r.setStoreError(err)
}

// Start recovers persisted scheduler ownership and reconciles the current
// configuration. Runtime construction remains inert so callers can finish
// dependency wiring before any automatic request is eligible.
func (r *Runtime) Start() {
	if r == nil {
		return
	}
	r.configMu.Lock()
	defer r.configMu.Unlock()
	r.mu.RLock()
	if r.stopped || r.started {
		r.mu.RUnlock()
		return
	}
	scheduler := r.scheduler
	config := cloneConfig(r.config)
	r.mu.RUnlock()
	r.mu.Lock()
	if r.stopped || r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	if r.deps.Audit != nil {
		if err := r.deps.Audit.RecoverPending(r.now()); err != nil {
			r.setStoreError(err)
		}
	}
	if scheduler == nil {
		return
	}
	scheduler.Start()
	if err := scheduler.Reconcile(config); err != nil {
		r.setStoreError(err)
	}
}

// Stop cancels active manual work, prevents future work from starting, and
// waits for all Runtime-owned goroutines.  It is safe to call repeatedly.
func (r *Runtime) Stop() {
	if r == nil {
		return
	}
	var cancel context.CancelFunc
	var stopCancel context.CancelFunc
	r.stopOnce.Do(func() {
		r.configMu.Lock()
		defer r.configMu.Unlock()
		r.mu.Lock()
		r.stopped = true
		close(r.stop)
		stopCancel = r.stopCancel
		if r.run != nil {
			cancel = r.run.cancel
		}
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if stopCancel != nil {
			stopCancel()
		}
		if r.scheduler != nil {
			r.scheduler.Stop()
		}
		// A reset that already passed the consume gate may finish against the
		// canceled context, but a reset that has not passed it must not issue
		// an upstream consume after shutdown begins.
		r.resetGate.Lock()
		r.resetGate.Unlock()
	})
	r.wg.Wait()
}

// Stopped reports whether Stop has completed its lifecycle transition. It is
// intentionally synchronized so embedders can verify replacement and
// shutdown without reaching into Runtime's orchestration state.
func (r *Runtime) Stopped() bool {
	if r == nil {
		return true
	}
	r.mu.RLock()
	stopped := r.stopped
	r.mu.RUnlock()
	return stopped
}

func (r *Runtime) acquireBusy(key string) bool {
	key = normalizeKey(key)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	if _, exists := r.busy[key]; exists {
		return false
	}
	r.busy[key] = struct{}{}
	return true
}

func (r *Runtime) isBusy(key string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	_, busy := r.busy[normalizeKey(key)]
	r.mu.RUnlock()
	return busy
}

func (r *Runtime) releaseBusy(key string) {
	r.mu.Lock()
	delete(r.busy, normalizeKey(key))
	r.mu.Unlock()
}

func normalizeKey(key string) string {
	return strings.TrimSpace(key)
}

// Schedule returns a copy of the current complete configuration. The copy is
// independent of Runtime so callers can edit a draft without racing the
// scheduler or changing the live configuration before an explicit update.
func (r *Runtime) Schedule() domain.Config {
	return r.configSnapshot()
}

// GetSchedule is an explicit alias for embedders that prefer getter naming.
func (r *Runtime) GetSchedule() domain.Config {
	return r.Schedule()
}

// ListAccounts exposes the host-owned account projection used by management.
// Authentication material is never part of accounts.Account.
func (r *Runtime) ListAccounts(ctx context.Context) ([]accounts.Account, error) {
	if r == nil || r.deps.Accounts == nil {
		return nil, context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.deps.Accounts.List(ctx)
}

// UpdateSchedule atomically replaces the persisted and live schedule. The
// configuration lock covers revision comparison, account discovery,
// validation, persistence, the in-memory swap, and scheduler reconciliation so
// two writers cannot validate or reconcile against different revisions.
func (r *Runtime) UpdateSchedule(ctx context.Context, draft domain.Config) (domain.Config, error) {
	if r == nil {
		return domain.Config{}, context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.configMu.Lock()
	defer r.configMu.Unlock()

	r.mu.RLock()
	current := cloneConfig(r.config)
	r.mu.RUnlock()
	if draft.Revision != current.Revision {
		return domain.Config{}, appError(domain.CodeRevisionConflict, 409, false, "schedule revision changed")
	}

	known, err := r.discoveredAccountKeys(ctx)
	if err != nil {
		return domain.Config{}, err
	}
	if draft.SchemaVersion == 0 {
		draft.SchemaVersion = current.SchemaVersion
		if draft.SchemaVersion == 0 {
			draft.SchemaVersion = domain.DefaultConfig().SchemaVersion
		}
	}
	if err := domain.ValidateConfig(draft, domain.ValidatePersisted, known); err != nil {
		return domain.Config{}, err
	}
	draft.Revision = current.Revision + 1

	if err := r.deps.Config.Save(draft); err != nil {
		r.setStoreError(err)
		return domain.Config{}, err
	}
	r.mu.Lock()
	r.config = cloneConfig(draft)
	r.mu.Unlock()
	if r.scheduler != nil {
		if err := r.scheduler.Reconcile(draft); err != nil {
			r.setStoreError(err)
			return draft, err
		}
	}
	r.clearStoreError()
	return cloneConfig(draft), nil
}

func (r *Runtime) discoveredAccountKeys(ctx context.Context) (map[string]struct{}, error) {
	accountsList, err := r.deps.Accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]struct{}, len(accountsList))
	for _, account := range accountsList {
		if key := normalizeKey(account.Key); key != "" {
			known[key] = struct{}{}
		}
	}
	return known, nil
}

// Simulate validates a draft against the currently discovered accounts and
// delegates all schedule math to the same pure simulator used by the runtime.
func (r *Runtime) Simulate(ctx context.Context, draft domain.Config) (domain.SimulationResult, error) {
	if r == nil {
		return domain.SimulationResult{}, context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	known, err := r.discoveredAccountKeys(ctx)
	if err != nil {
		return domain.SimulationResult{}, err
	}
	// Simulation is a what-if preview and must remain usable before the user
	// has selected real scheduled accounts.  Use an internal planner-only
	// account in that case; it is never persisted or sent to the runtime.
	if len(draft.ScheduledAccountKeys) == 0 {
		known["__simulation__"] = struct{}{}
		draft.ScheduledAccountKeys = []string{"__simulation__"}
	}
	if draft.PreheatLeadMinutes == nil || draft.PreheatSpanMinutes == nil {
		lead, span := 120, 60
		draft.PreheatLeadMinutes, draft.PreheatSpanMinutes = &lead, &span
	}
	if err := domain.ValidateConfig(draft, domain.ValidateSimulation, known); err != nil {
		return domain.SimulationResult{}, err
	}
	location, err := time.LoadLocation(draft.Timezone)
	if err != nil {
		return domain.SimulationResult{}, err
	}
	return (simulate.Service{}).Run(draft, r.now().In(location))
}

// ListHistory reads the bounded operation history without exposing the
// repository implementation or filesystem errors to transport callers.
func (r *Runtime) ListHistory() ([]domain.OperationRecord, error) {
	if r == nil || r.deps.History == nil {
		return nil, context.Canceled
	}
	records, err := r.deps.History.Load()
	if err != nil {
		r.setStoreError(err)
	}
	return records, err
}

// ClearHistory removes ordinary operation history and the in-memory quota
// snapshots. It deliberately leaves configuration, runtime state, and reset
// audit persistence untouched.
func (r *Runtime) ClearHistory() error {
	if r == nil || r.deps.History == nil {
		return context.Canceled
	}
	if err := r.deps.History.Clear(); err != nil {
		r.setStoreError(err)
		return err
	}
	if clearer, ok := r.deps.Quota.(interface{ ClearSnapshots() }); ok {
		clearer.ClearSnapshots()
	}
	r.clearStoreError()
	return nil
}

// ListQuota returns only cached quota views. It discovers account identities
// through the host metadata boundary but never performs an upstream refresh.
func (r *Runtime) ListQuota(ctx context.Context) ([]domain.SnapshotView, error) {
	if r == nil || r.deps.Accounts == nil || r.deps.Quota == nil {
		return nil, context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	accountsList, err := r.deps.Accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	now := r.now()
	views := make([]domain.SnapshotView, 0, len(accountsList))
	for _, account := range accountsList {
		key := normalizeKey(account.Key)
		if key == "" {
			continue
		}
		if view, ok := r.deps.Quota.Get(key, now); ok {
			if view.Snapshot.AccountKey == "" {
				view.Snapshot.AccountKey = key
			}
			views = append(views, view)
			continue
		}
		views = append(views, domain.SnapshotView{Snapshot: domain.UsageSnapshot{AccountKey: key}})
	}
	return views, nil
}

// CurrentQuota is an explicit alias for management adapters that use noun
// naming. It retains the same memory-only behavior as ListQuota.
func (r *Runtime) CurrentQuota(ctx context.Context) ([]domain.SnapshotView, error) {
	return r.ListQuota(ctx)
}
