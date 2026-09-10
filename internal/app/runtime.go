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

	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/schedule"
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
	mu        sync.RWMutex
	deps      Dependencies
	config    domain.Config
	run       *runState
	busy      map[string]struct{}
	stop      chan struct{}
	wg        sync.WaitGroup
	stopOnce  sync.Once
	stopped   bool
	scheduler *schedule.Scheduler

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
	runtime := &Runtime{
		deps:   deps,
		config: cloneConfig(config),
		busy:   make(map[string]struct{}),
		stop:   make(chan struct{}),
	}
	if err != nil {
		if domain.CodeOf(err) == domain.CodeStoreCorrupt {
			runtime.config = domain.DefaultConfig()
			runtime.storeErrorCode = domain.CodeStoreCorrupt
			return runtime, nil
		}
		return nil, err
	}
	planner := deps.Planner
	if planner == nil {
		planner = schedule.Planner{}
	}
	runtime.scheduler = schedule.NewScheduler(deps.Clock, planner, deps.State, runtime)
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

// Start recovers persisted scheduler ownership and reconciles the current
// configuration. Runtime construction remains inert so callers can finish
// dependency wiring before any automatic request is eligible.
func (r *Runtime) Start() {
	if r == nil {
		return
	}
	r.mu.RLock()
	if r.stopped {
		r.mu.RUnlock()
		return
	}
	scheduler := r.scheduler
	config := cloneConfig(r.config)
	r.mu.RUnlock()
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
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.stopped = true
		close(r.stop)
		if r.run != nil {
			cancel = r.run.cancel
		}
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if r.scheduler != nil {
			r.scheduler.Stop()
		}
	})
	r.wg.Wait()
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
