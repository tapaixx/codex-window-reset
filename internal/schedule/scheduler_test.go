package schedule

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type schedulerTestTimer struct {
	mu      sync.Mutex
	clock   *schedulerTestClock
	due     time.Time
	fn      func()
	stopped bool
}

func (t *schedulerTestTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

func (t *schedulerTestTimer) ready(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.stopped && !t.due.After(now)
}

func (t *schedulerTestTimer) run() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	fn := t.fn
	t.mu.Unlock()
	fn()
}

type schedulerTestClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*schedulerTestTimer
}

func newSchedulerTestClock(now time.Time) *schedulerTestClock {
	return &schedulerTestClock{now: now.UTC()}
}

func (c *schedulerTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *schedulerTestClock) AfterFunc(delay time.Duration, fn func()) domain.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &schedulerTestTimer{clock: c, due: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *schedulerTestClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	now := c.now
	c.mu.Unlock()
	for {
		var ready *schedulerTestTimer
		c.mu.Lock()
		for _, timer := range c.timers {
			if timer.ready(now) {
				ready = timer
				break
			}
		}
		c.mu.Unlock()
		if ready == nil {
			return
		}
		ready.run()
	}
}

type schedulerTestStateRepository struct {
	mu           sync.Mutex
	state        domain.RuntimeState
	updateErrors []error
	loadErrors   []error
}

func (r *schedulerTestStateRepository) Load() (domain.RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.loadErrors) > 0 {
		err := r.loadErrors[0]
		r.loadErrors = r.loadErrors[1:]
		return domain.RuntimeState{}, err
	}
	return cloneSchedulerState(r.state), nil
}

func (r *schedulerTestStateRepository) Save(state domain.RuntimeState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = cloneSchedulerState(state)
	return nil
}

func (r *schedulerTestStateRepository) Update(mutate func(*domain.RuntimeState) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.updateErrors) > 0 {
		err := r.updateErrors[0]
		r.updateErrors = r.updateErrors[1:]
		return err
	}
	state := cloneSchedulerState(r.state)
	if err := mutate(&state); err != nil {
		return err
	}
	r.state = state
	return nil
}

func (r *schedulerTestStateRepository) saved() domain.RuntimeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSchedulerState(r.state)
}

func (r *schedulerTestStateRepository) FailNextLoad(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadErrors = append(r.loadErrors, err)
}

func (r *schedulerTestStateRepository) FailNextUpdate(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateErrors = append(r.updateErrors, err)
}

func cloneSchedulerState(state domain.RuntimeState) domain.RuntimeState {
	copy := state
	copy.Occurrences = make(map[string]domain.OccurrenceState, len(state.Occurrences))
	for key, value := range state.Occurrences {
		copy.Occurrences[key] = value
	}
	copy.NextRuns = make(map[string]time.Time, len(state.NextRuns))
	for key, value := range state.NextRuns {
		copy.NextRuns[key] = value
	}
	copy.GuardrailHolds = make(map[string]domain.GuardrailHold, len(state.GuardrailHolds))
	for key, value := range state.GuardrailHolds {
		copy.GuardrailHolds[key] = value
	}
	return copy
}

type schedulerTestPlanner struct {
	mu           sync.Mutex
	plans        map[string][]domain.PlannedOccurrence
	errors       map[string]error
	calls        int
	blockedDate  string
	blocked      chan struct{}
	blockRelease <-chan struct{}
}

func (p *schedulerTestPlanner) PlanDay(_ domain.Config, date time.Time) ([]domain.PlannedOccurrence, error) {
	p.mu.Lock()
	p.calls++
	dateKey := date.Format("2006-01-02")
	plans := append([]domain.PlannedOccurrence(nil), p.plans[dateKey]...)
	err := p.errors[dateKey]
	blocked := p.blockedDate == dateKey
	entered, release := p.blocked, p.blockRelease
	p.mu.Unlock()
	if blocked {
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		if release != nil {
			<-release
		}
	}
	return plans, err
}

type schedulerTestExecutor struct {
	mu                sync.Mutex
	calls             []domain.PlannedOccurrence
	observedErrors    []error
	results           []domain.OperationRecord
	entered           chan struct{}
	onCall            func(domain.PlannedOccurrence)
	onCallDone        chan struct{}
	returnGate        chan struct{}
	allowCompensation bool
}

func (e *schedulerTestExecutor) ExecutePreheat(_ context.Context, occurrence domain.PlannedOccurrence) domain.OperationRecord {
	e.mu.Lock()
	e.calls = append(e.calls, occurrence)
	onCall := e.onCall
	if e.entered != nil {
		select {
		case e.entered <- struct{}{}:
		default:
		}
	}
	record := domain.OperationRecord{
		Trigger:        domain.TriggerPreheat,
		OccurrenceID:   occurrence.ID,
		AccountKey:     occurrence.AccountKey,
		RequestOutcome: domain.RequestSucceeded,
		WindowOutcome:  domain.WindowVerifiedStarted,
	}
	if len(e.results) > 0 {
		record = e.results[0]
		e.results = e.results[1:]
	}
	e.mu.Unlock()
	if onCall != nil {
		onCall(occurrence)
	}
	if e.onCallDone != nil {
		select {
		case e.onCallDone <- struct{}{}:
		default:
		}
	}
	if e.returnGate != nil {
		<-e.returnGate
	}
	return record
}

func (e *schedulerTestExecutor) ReportSchedulerError(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observedErrors = append(e.observedErrors, err)
}

func (e *schedulerTestExecutor) CompensationEligible(context.Context, domain.PlannedOccurrence) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.allowCompensation
}

func (e *schedulerTestExecutor) Calls() []domain.PlannedOccurrence {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]domain.PlannedOccurrence(nil), e.calls...)
}

func (e *schedulerTestExecutor) Errors() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]error(nil), e.observedErrors...)
}

func schedulerTestConfig() domain.Config {
	return domain.Config{Enabled: true, Timezone: "UTC", ScheduledAccountKeys: []string{"a"}}
}

func TestStartupMarksEveryUnexecutedPastPlanMissed(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	state := domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{
		"2026-09-09/p0/a": {PlannedOccurrence: domain.PlannedOccurrence{ID: "2026-09-09/p0/a", PlannedAt: schedulerInstant("2026-09-09T06:45:00Z")}, Status: domain.OccurrencePlanned},
	}}
	fx := newSchedulerFixture(t, now, state)
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	got := fx.states.saved().Occurrences["2026-09-09/p0/a"]
	if got.Status != domain.OccurrenceMissed || len(fx.executor.Calls()) != 0 {
		t.Fatalf("state=%#v calls=%d", got, len(fx.executor.Calls()))
	}
}

func TestFutureOccurrenceFiresExactlyOnce(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	occurrence := domain.PlannedOccurrence{
		ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PeriodIndex: 0,
		PlannedAt: now.Add(time.Hour), WindowStart: now.Add(-time.Hour), WindowEnd: now.Add(2 * time.Hour),
	}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans["2026-09-09"] = []domain.PlannedOccurrence{occurrence}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	fx.clock.Advance(time.Hour)
	select {
	case <-fx.executor.entered:
	case <-time.After(time.Second):
		t.Fatalf("executor calls=%d, want 1", len(fx.executor.Calls()))
	}
	deadline := time.After(time.Second)
	for fx.states.saved().Occurrences[occurrence.ID].Status != domain.OccurrenceSucceeded {
		select {
		case <-deadline:
			t.Fatalf("state after callback=%#v", fx.states.saved().Occurrences[occurrence.ID])
		default:
			time.Sleep(time.Millisecond)
		}
	}
	fx.clock.Advance(time.Hour)
	if got := len(fx.executor.Calls()); got != 1 {
		t.Fatalf("executor calls after second advance=%d (%#v), want 1", got, fx.executor.Calls())
	}
	got := fx.states.saved().Occurrences[occurrence.ID]
	if got.Status != domain.OccurrenceSucceeded {
		t.Fatalf("status=%s, want succeeded", got.Status)
	}
}

func TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	oldPlan := domain.PlannedOccurrence{
		ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PeriodIndex: 0,
		PlannedAt: now.Add(time.Hour), WindowStart: now, WindowEnd: now.Add(2 * time.Hour),
	}
	newPlan := oldPlan
	newPlan.PlannedAt = now.Add(3 * time.Hour)
	newPlan.WindowStart = now.Add(2 * time.Hour)
	newPlan.WindowEnd = now.Add(4 * time.Hour)
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans[oldPlan.LocalDate] = []domain.PlannedOccurrence{oldPlan}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	if got := fx.states.saved().Occurrences[oldPlan.ID].PlannedAt; !got.Equal(oldPlan.PlannedAt) {
		t.Fatalf("initial planned_at=%s, want %s", got, oldPlan.PlannedAt)
	}

	fx.planner.plans[oldPlan.LocalDate] = []domain.PlannedOccurrence{newPlan}
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	saved := fx.states.saved()
	if got := saved.Occurrences[newPlan.ID].PlannedAt; !got.Equal(newPlan.PlannedAt) {
		t.Fatalf("refreshed planned_at=%s, want %s", got, newPlan.PlannedAt)
	}
	if got := saved.NextRuns[newPlan.ID]; !got.Equal(newPlan.PlannedAt) {
		t.Fatalf("refreshed next_run=%s, want %s", got, newPlan.PlannedAt)
	}

	fx.clock.Advance(time.Hour)
	if got := len(fx.executor.Calls()); got != 0 {
		t.Fatalf("stale timer executed %d operations", got)
	}
	fx.clock.Advance(2 * time.Hour)
	select {
	case <-fx.executor.entered:
	case <-time.After(time.Second):
		t.Fatalf("refreshed timer did not execute; calls=%#v", fx.executor.Calls())
	}
	calls := fx.executor.Calls()
	if len(calls) != 1 || !calls[0].PlannedAt.Equal(newPlan.PlannedAt) {
		t.Fatalf("calls=%#v, want one call for refreshed plan %#v", calls, newPlan)
	}
}

func TestReconcileReplacesMidnightTimerWhenTimezoneChanges(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	utc := schedulerTestConfig()
	if err := fx.scheduler.Reconcile(utc); err != nil {
		t.Fatal(err)
	}
	if !schedulerClockHasActiveTimer(fx.clock, schedulerInstant("2026-09-10T00:00:00Z")) {
		t.Fatalf("initial UTC midnight timer was not installed")
	}

	pacific := utc
	pacific.Timezone = "America/Los_Angeles"
	if err := fx.scheduler.Reconcile(pacific); err != nil {
		t.Fatal(err)
	}
	if !schedulerClockHasActiveTimer(fx.clock, schedulerInstant("2026-09-09T07:00:00Z")) {
		t.Fatalf("midnight timer was not replaced for new timezone")
	}
}

func TestQueuedCallbackAfterStopDoesNotExecute(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	occurrence := domain.PlannedOccurrence{
		ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PeriodIndex: 0,
		PlannedAt: now.Add(time.Hour), WindowStart: now, WindowEnd: now.Add(2 * time.Hour),
	}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans[occurrence.LocalDate] = []domain.PlannedOccurrence{occurrence}
	fx.scheduler.Start()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	fx.scheduler.Stop()
	fx.clock.Advance(time.Hour)

	// Model a timer callback already queued at the ownership boundary after
	// Stop returned. The handler must fence it before any executor I/O.
	fx.scheduler.handle(schedulerCommand{kind: commandOccurrence, id: occurrence.ID})
	if got := len(fx.executor.Calls()); got != 0 {
		t.Fatalf("queued callback executed %d operations after Stop", got)
	}
}

func TestSchedulerReportsOccurrencePersistenceAndReconcileFailures(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		now := schedulerInstant("2026-09-09T06:30:00Z")
		occurrence := domain.PlannedOccurrence{ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PlannedAt: now.Add(time.Minute)}
		fx := newSchedulerFixture(t, now, domain.RuntimeState{})
		fx.planner.plans[occurrence.LocalDate] = []domain.PlannedOccurrence{occurrence}
		fx.scheduler.Start()
		defer fx.scheduler.Stop()
		if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
			t.Fatal(err)
		}
		fx.states.FailNextUpdate(errors.New("claim persistence failed"))
		fx.clock.Advance(time.Minute)
		waitForSchedulerError(t, fx.executor)
		if got := len(fx.executor.Calls()); got != 0 {
			t.Fatalf("executor calls=%d after failed claim", got)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		now := schedulerInstant("2026-09-09T06:30:00Z")
		occurrence := domain.PlannedOccurrence{ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PlannedAt: now.Add(time.Minute)}
		fx := newSchedulerFixture(t, now, domain.RuntimeState{})
		fx.planner.plans[occurrence.LocalDate] = []domain.PlannedOccurrence{occurrence}
		fx.executor.returnGate = make(chan struct{})
		fx.scheduler.Start()
		defer fx.scheduler.Stop()
		if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
			t.Fatal(err)
		}
		fx.clock.Advance(time.Minute)
		select {
		case <-fx.executor.entered:
		case <-time.After(time.Second):
			t.Fatal("occurrence did not claim")
		}
		fx.states.FailNextUpdate(errors.New("terminal persistence failed"))
		close(fx.executor.returnGate)
		waitForSchedulerError(t, fx.executor)
	})

	t.Run("post-callback reconcile", func(t *testing.T) {
		now := schedulerInstant("2026-09-09T06:30:00Z")
		occurrence := domain.PlannedOccurrence{ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PlannedAt: now.Add(time.Minute)}
		fx := newSchedulerFixture(t, now, domain.RuntimeState{})
		fx.planner.plans[occurrence.LocalDate] = []domain.PlannedOccurrence{occurrence}
		fx.scheduler.Start()
		defer fx.scheduler.Stop()
		if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
			t.Fatal(err)
		}
		fx.planner.mu.Lock()
		fx.planner.errors[occurrence.LocalDate] = errors.New("reconciliation failed")
		fx.planner.mu.Unlock()
		fx.clock.Advance(time.Minute)
		waitForSchedulerError(t, fx.executor)
	})
}

func TestSchedulerReportsCompensationLoadFailure(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	id := "2026-09-09/p0/a"
	due := now.Add(time.Minute)
	occurrence := domain.PlannedOccurrence{ID: id, AccountKey: "a", LocalDate: "2026-09-09", PlannedAt: now.Add(-time.Hour)}
	state := domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{
		id: {PlannedOccurrence: occurrence, Status: domain.OccurrenceFailed, CompensationDueAt: due},
	}}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans[occurrence.LocalDate] = []domain.PlannedOccurrence{occurrence}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.states.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	fx.states.FailNextLoad(errors.New("compensation load failed"))
	fx.clock.Advance(time.Minute)
	waitForSchedulerError(t, fx.executor)
	if got := len(fx.executor.Calls()); got != 0 {
		t.Fatalf("executor calls=%d after failed compensation load", got)
	}
}

func TestReconcilePlansTodayAndSevenFollowingLocalDates(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	for offset := 0; offset <= schedulerHorizonDays; offset++ {
		date := now.AddDate(0, 0, offset).Format("2006-01-02")
		planned := now.Add(time.Duration(offset+1) * time.Hour)
		fx.planner.plans[date] = []domain.PlannedOccurrence{{
			ID: "occ-" + date, AccountKey: "a", LocalDate: date, PlannedAt: planned,
		}}
	}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	saved := fx.states.saved()
	if len(saved.Occurrences) != schedulerHorizonDays+1 || len(saved.NextRuns) != schedulerHorizonDays+1 {
		t.Fatalf("occurrences=%d next_runs=%d state=%#v", len(saved.Occurrences), len(saved.NextRuns), saved)
	}
	fx.planner.mu.Lock()
	calls := fx.planner.calls
	fx.planner.mu.Unlock()
	if calls != schedulerHorizonDays+1 {
		t.Fatalf("planner calls=%d, want %d", calls, schedulerHorizonDays+1)
	}
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	if got := len(fx.states.saved().Occurrences); got != schedulerHorizonDays+1 {
		t.Fatalf("second reconcile duplicated occurrences: %d", got)
	}
}

func TestDisabledReconcileCancelsFutureTimersWithoutChangingSelectionHistory(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	occurrence := domain.PlannedOccurrence{ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PlannedAt: now.Add(time.Hour)}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans["2026-09-09"] = []domain.PlannedOccurrence{occurrence}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	disabled := schedulerTestConfig()
	disabled.Enabled = false
	if err := fx.scheduler.Reconcile(disabled); err != nil {
		t.Fatal(err)
	}
	fx.clock.Advance(2 * time.Hour)
	if got := len(fx.executor.Calls()); got != 0 {
		t.Fatalf("disabled scheduler executed %d operations", got)
	}
	state := fx.states.saved()
	if _, ok := state.NextRuns[occurrence.ID]; ok {
		t.Fatalf("disabled scheduler retained next run: %#v", state.NextRuns)
	}
	if got := disabled.ScheduledAccountKeys; len(got) != 1 || got[0] != "a" {
		t.Fatalf("disabled scheduler changed account selection: %#v", got)
	}
}

func TestMidnightCallbackAdvancesPlanningHorizon(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	occurrence := domain.PlannedOccurrence{ID: "2026-09-17/p0/a", AccountKey: "a", LocalDate: "2026-09-17", PlannedAt: now.Add(8 * 24 * time.Hour)}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans[occurrence.LocalDate] = []domain.PlannedOccurrence{occurrence}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	if _, ok := fx.states.saved().Occurrences[occurrence.ID]; ok {
		t.Fatalf("occurrence outside initial horizon was planned early: %#v", fx.states.saved().Occurrences[occurrence.ID])
	}
	fx.clock.Advance(17*time.Hour + 30*time.Minute)
	deadline := time.After(time.Second)
	for {
		if _, ok := fx.states.saved().Occurrences[occurrence.ID]; ok {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("midnight did not reconcile horizon: %#v", fx.states.saved())
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestCompensationFiresOnceWhileProcessOwnsTimer(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	occurrence := domain.PlannedOccurrence{
		ID: "2026-09-09/p0/a", AccountKey: "a", LocalDate: "2026-09-09", PeriodIndex: 0,
		PlannedAt: now.Add(time.Minute), WindowStart: now, WindowEnd: now.Add(2 * time.Hour),
	}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.executor.results = []domain.OperationRecord{{RequestOutcome: domain.RequestNetworkError}, {RequestOutcome: domain.RequestSucceeded}}
	fx.executor.onCall = func(value domain.PlannedOccurrence) {
		if len(fx.executor.Calls()) != 1 {
			return
		}
		_ = fx.states.Update(func(state *domain.RuntimeState) error {
			entry := state.Occurrences[value.ID]
			entry.CompensationDueAt = fx.clock.Now().Add(5 * time.Minute)
			state.Occurrences[value.ID] = entry
			return nil
		})
	}
	fx.executor.onCallDone = make(chan struct{}, 1)
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	fx.planner.plans["2026-09-09"] = []domain.PlannedOccurrence{occurrence}
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	fx.clock.Advance(time.Minute)
	select {
	case <-fx.executor.entered:
	case <-time.After(time.Second):
		t.Fatal("preheat did not execute")
	}
	select {
	case <-fx.executor.onCallDone:
	case <-time.After(time.Second):
		t.Fatal("preheat compensation state was not persisted")
	}
	due := waitForCompensationSchedule(t, fx.states, occurrence.ID)
	waitForClockTimer(t, fx.clock, due)
	fx.clock.Advance(5 * time.Minute)
	fx.clock.Advance(0)
	deadline := time.After(time.Second)
	for len(fx.executor.Calls()) < 2 {
		select {
		case <-deadline:
			t.Fatalf("compensation did not execute; calls=%d", len(fx.executor.Calls()))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	fx.clock.Advance(time.Hour)
	if got := len(fx.executor.Calls()); got != 2 {
		t.Fatalf("executor calls=%d, want one preheat and one compensation", got)
	}
	state := fx.states.saved().Occurrences[occurrence.ID]
	if state.Status != domain.OccurrenceSucceeded || !state.CompensationAttempted || !state.CompensationDueAt.IsZero() {
		t.Fatalf("state after compensation=%#v", state)
	}
}

func TestRestartMarksPendingCompensationMissed(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	id := "2026-09-09/p0/a"
	state := domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{
		id: {
			PlannedOccurrence: domain.PlannedOccurrence{ID: id, AccountKey: "a", PlannedAt: now.Add(-time.Hour)},
			Status:            domain.OccurrenceFailed,
			CompensationDueAt: now.Add(5 * time.Minute),
		},
	}}
	fx := newSchedulerFixture(t, now, state)
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	got := fx.states.saved().Occurrences[id]
	if got.Status != domain.OccurrenceMissed || !got.Missed || !got.CompensationDueAt.IsZero() {
		t.Fatalf("state=%#v", got)
	}
	if got := len(fx.executor.Calls()); got != 0 {
		t.Fatalf("executor calls=%d, want 0", got)
	}
}

func TestCompensationEligibilityIsRecheckedBeforeAttempt(t *testing.T) {
	now := schedulerInstant("2026-09-09T06:30:00Z")
	occurrence := domain.PlannedOccurrence{ID: "2026-09-09/p0/a", AccountKey: "a", PlannedAt: now.Add(time.Minute)}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.executor.results = []domain.OperationRecord{{RequestOutcome: domain.RequestNetworkError}}
	fx.executor.onCall = func(value domain.PlannedOccurrence) {
		_ = fx.states.Update(func(state *domain.RuntimeState) error {
			entry := state.Occurrences[value.ID]
			entry.CompensationDueAt = fx.clock.Now().Add(time.Minute)
			state.Occurrences[value.ID] = entry
			return nil
		})
	}
	fx.executor.onCallDone = make(chan struct{}, 1)
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	fx.planner.plans["2026-09-09"] = []domain.PlannedOccurrence{occurrence}
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}
	fx.clock.Advance(time.Minute)
	select {
	case <-fx.executor.entered:
	case <-time.After(time.Second):
		t.Fatal("preheat did not execute")
	}
	select {
	case <-fx.executor.onCallDone:
	case <-time.After(time.Second):
		t.Fatal("preheat compensation state was not persisted")
	}
	due := waitForCompensationSchedule(t, fx.states, occurrence.ID)
	waitForClockTimer(t, fx.clock, due)
	fx.executor.mu.Lock()
	fx.executor.allowCompensation = false
	fx.executor.mu.Unlock()
	fx.clock.Advance(time.Minute)
	fx.clock.Advance(0)
	deadline := time.After(time.Second)
	for {
		got := fx.states.saved().Occurrences[occurrence.ID]
		if got.CompensationAttempted {
			if got.Status != domain.OccurrenceSkipped || len(fx.executor.Calls()) != 1 {
				t.Fatalf("ineligible compensation state=%#v calls=%d", got, len(fx.executor.Calls()))
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("compensation eligibility was not evaluated")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

type schedulerFixture struct {
	clock     *schedulerTestClock
	planner   *schedulerTestPlanner
	states    *schedulerTestStateRepository
	executor  *schedulerTestExecutor
	scheduler *Scheduler
}

func newSchedulerFixture(t *testing.T, now time.Time, state domain.RuntimeState) schedulerFixture {
	t.Helper()
	clock := newSchedulerTestClock(now)
	planner := &schedulerTestPlanner{
		plans:  make(map[string][]domain.PlannedOccurrence),
		errors: make(map[string]error),
	}
	states := &schedulerTestStateRepository{state: cloneSchedulerState(state)}
	executor := &schedulerTestExecutor{entered: make(chan struct{}, 8), allowCompensation: true}
	scheduler := NewScheduler(clock, planner, states, executor)
	if scheduler == nil {
		t.Fatal("NewScheduler returned nil")
	}
	return schedulerFixture{clock: clock, planner: planner, states: states, executor: executor, scheduler: scheduler}
}

func schedulerInstant(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func waitForCompensationSchedule(t *testing.T, states *schedulerTestStateRepository, id string) time.Time {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		state := states.saved()
		if due, ok := state.NextRuns[id]; ok {
			return due
		}
		select {
		case <-deadline:
			t.Fatalf("compensation was not scheduled: %#v", state.Occurrences[id])
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func waitForClockTimer(t *testing.T, clock *schedulerTestClock, due time.Time) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		clock.mu.Lock()
		found := false
		for _, timer := range clock.timers {
			timer.mu.Lock()
			if !timer.stopped && timer.due.Equal(due) {
				found = true
			}
			timer.mu.Unlock()
			if found {
				break
			}
		}
		clock.mu.Unlock()
		if found {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("compensation timer was not installed for %s", due)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func schedulerClockHasActiveTimer(clock *schedulerTestClock, due time.Time) bool {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	for _, timer := range clock.timers {
		timer.mu.Lock()
		active := !timer.stopped && timer.due.Equal(due)
		timer.mu.Unlock()
		if active {
			return true
		}
	}
	return false
}

func waitForSchedulerError(t *testing.T, executor *schedulerTestExecutor) error {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		errors := executor.Errors()
		if len(errors) > 0 {
			return errors[0]
		}
		select {
		case <-deadline:
			t.Fatal("scheduler failure was not reported")
			return nil
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
