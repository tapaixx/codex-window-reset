package schedule

import (
	"context"
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
	mu    sync.Mutex
	state domain.RuntimeState
}

func (r *schedulerTestStateRepository) Load() (domain.RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	mu    sync.Mutex
	plans map[string][]domain.PlannedOccurrence
	calls int
}

func (p *schedulerTestPlanner) PlanDay(_ domain.Config, date time.Time) ([]domain.PlannedOccurrence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	dateKey := date.Format("2006-01-02")
	return append([]domain.PlannedOccurrence(nil), p.plans[dateKey]...), nil
}

type schedulerTestExecutor struct {
	mu                sync.Mutex
	calls             []domain.PlannedOccurrence
	results           []domain.OperationRecord
	entered           chan struct{}
	onCall            func(domain.PlannedOccurrence)
	onCallDone        chan struct{}
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
	return record
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
	occurrence := domain.PlannedOccurrence{ID: "2026-09-10/p0/a", AccountKey: "a", LocalDate: "2026-09-10", PlannedAt: now.Add(48 * time.Hour)}
	fx := newSchedulerFixture(t, now, domain.RuntimeState{})
	fx.planner.plans["2026-09-10"] = []domain.PlannedOccurrence{occurrence}
	fx.scheduler.Start()
	defer fx.scheduler.Stop()
	if err := fx.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
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
	planner := &schedulerTestPlanner{plans: make(map[string][]domain.PlannedOccurrence)}
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
