package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

const (
	schedulerHorizonDays = 7
	schedulerRetryDelay  = time.Second
	midnightTimerID      = "@schedule-midnight"
	compensationPrefix   = "@compensation/"
)

// RuntimeStateRepository is the durable state boundary shared by the
// scheduler and quota service. Update must perform a read-modify-write as one
// transaction; this is what prevents a quota hold update from losing an
// occurrence transition (and vice versa).
type RuntimeStateRepository interface {
	Load() (domain.RuntimeState, error)
	Save(domain.RuntimeState) error
	Update(func(*domain.RuntimeState) error) error
}

// PlanSource is intentionally small so the scheduler can be tested with a
// deterministic plan source while production uses Planner from this package.
type PlanSource interface {
	PlanDay(domain.Config, time.Time) ([]domain.PlannedOccurrence, error)
}

// Executor runs one preheat request. Runtime implements this interface. A
// scheduler never performs host or network I/O itself; the executor owns that
// boundary and returns a durable operation result.
type Executor interface {
	ExecutePreheat(context.Context, domain.PlannedOccurrence) domain.OperationRecord
}

// CompensationEligibility is optional. Runtime implements it to re-read
// current account/config/guardrail state immediately before the one allowed
// compensation attempt. Small embedders and scheduler tests may omit it; in
// that case the Executor remains the eligibility boundary.
type CompensationEligibility interface {
	CompensationEligible(context.Context, domain.PlannedOccurrence) bool
}

// CompensationExecutor is optional. It lets Runtime label a compensation
// operation distinctly while the base Executor contract remains compatible
// with the preheat use case.
type CompensationExecutor interface {
	ExecuteCompensation(context.Context, domain.PlannedOccurrence) domain.OperationRecord
}

// schedulerErrorObserver is intentionally unexported. Runtime implements the
// narrow method so asynchronous timer failures become visible through its
// management status without expanding the scheduler's public API.
type schedulerErrorObserver interface {
	ReportSchedulerError(error)
}

// Scheduler owns every timer and serializes all state transitions through one
// goroutine. Reconciliation is event-driven: it runs on startup/config
// changes, at local midnight, and after each occurrence callback. There is no
// polling loop and no catch-up path.
type Scheduler struct {
	clock   domain.Clock
	planner PlanSource
	states  RuntimeStateRepository
	exec    Executor

	lifecycleMu sync.Mutex
	started     bool
	stopping    bool
	stopped     bool
	startErr    error
	commands    chan schedulerCommand
	stop        chan struct{}
	done        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	executionMu sync.Mutex

	// timers is accessed only by the owner goroutine after Start. The
	// lifecycle mutex protects cleanup that can happen concurrently with Stop.
	timers          map[string]domain.Timer
	timerDue        map[string]time.Time
	timerGeneration map[string]uint64
	config          domain.Config
}

type schedulerCommand struct {
	kind       schedulerCommandKind
	id         string
	generation uint64
	config     domain.Config
	result     chan error
}

type schedulerCommandKind uint8

const (
	commandReconcile schedulerCommandKind = iota + 1
	commandOccurrence
	commandCompensation
	commandMidnight
)

// NewScheduler constructs an inert scheduler. Call Start before relying on
// persisted startup recovery; Reconcile also starts it lazily for embedders
// that prefer a single setup call.
func NewScheduler(clock domain.Clock, planner PlanSource, states RuntimeStateRepository, executor Executor) *Scheduler {
	if clock == nil {
		clock = wallClock{}
	}
	return &Scheduler{
		clock:           clock,
		planner:         planner,
		states:          states,
		exec:            executor,
		timers:          make(map[string]domain.Timer),
		timerDue:        make(map[string]time.Time),
		timerGeneration: make(map[string]uint64),
	}
}

// Start recovers ownership synchronously before exposing timer callbacks. Any
// occurrence left planned/running by a previous process is marked missed;
// this deliberately includes occurrences whose nominal window has not yet
// elapsed, so a restart can never catch up changed timing. A pending
// compensation also loses its prior timer ownership and is marked missed.
func (s *Scheduler) Start() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	if s.started || s.stopped {
		s.lifecycleMu.Unlock()
		return
	}
	s.started = true
	s.commands = make(chan schedulerCommand, 32)
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.lifecycleMu.Unlock()

	if s.states != nil {
		startErr := s.states.Update(func(state *domain.RuntimeState) error {
			ensureRuntimeStateMaps(state)
			for id, occurrence := range state.Occurrences {
				if occurrence.Status == domain.OccurrencePlanned || occurrence.Status == domain.OccurrenceRunning ||
					(!occurrence.CompensationAttempted && !occurrence.CompensationDueAt.IsZero()) {
					markMissed(&occurrence, "restart")
					state.Occurrences[id] = occurrence
				}
			}
			return nil
		})
		s.lifecycleMu.Lock()
		s.startErr = startErr
		s.lifecycleMu.Unlock()
		if startErr != nil {
			s.reportError(fmt.Errorf("scheduler startup recovery: %w", startErr))
		}
	}

	go s.ownerLoop()
}

// Stop cancels timers and the owner goroutine. It is safe to call repeatedly.
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	if !s.started || s.stopping || s.stopped {
		done := s.done
		if !s.started {
			s.stopped = true
		}
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	s.stopping = true
	stop := s.stop
	done := s.done
	cancel := s.cancel
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	// An executor that already owns the execution gate is allowed to finish
	// against the canceled context. No new executor can pass the gate after
	// stopping is set, so the final stopped state is a hard I/O fence.
	s.executionMu.Lock()
	s.executionMu.Unlock()
	s.lifecycleMu.Lock()
	s.stopped = true
	s.lifecycleMu.Unlock()
	close(stop)
	<-done
}

// Reconcile plans today and the following seven local calendar dates. It is
// safe to call from any goroutine; the owner performs all persistence and
// timer changes in order. A disabled configuration only cancels active
// timers, preserving Scheduled Account membership and terminal history.
func (s *Scheduler) Reconcile(config domain.Config) error {
	if s == nil {
		return errors.New("scheduler is nil")
	}
	s.Start()
	s.lifecycleMu.Lock()
	startErr := s.startErr
	started, stopped := s.started, s.stopped
	commands, done := s.commands, s.done
	s.lifecycleMu.Unlock()
	if startErr != nil {
		return startErr
	}
	if !started || stopped {
		return errors.New("scheduler is stopped")
	}
	result := make(chan error, 1)
	command := schedulerCommand{kind: commandReconcile, config: cloneConfig(config), result: result}
	select {
	case commands <- command:
	case <-done:
		return errors.New("scheduler is stopped")
	}
	select {
	case err := <-result:
		return err
	case <-done:
		return errors.New("scheduler is stopped")
	}
}

func (s *Scheduler) ownerLoop() {
	defer close(s.done)
	for {
		select {
		case command := <-s.commands:
			s.handle(command)
		case <-s.stop:
			s.stopTimers()
			return
		case <-s.ctx.Done():
			s.stopTimers()
			return
		}
	}
}

func (s *Scheduler) handle(command schedulerCommand) {
	if s.isStopped() {
		if command.result != nil {
			command.result <- errors.New("scheduler is stopped")
		}
		return
	}
	var err error
	switch command.kind {
	case commandReconcile:
		s.config = cloneConfig(command.config)
		err = s.reconcileOwned(command.config)
	case commandOccurrence:
		err = s.handleOccurrence(command.id, command.generation)
	case commandCompensation:
		err = s.handleCompensation(command.id, command.generation)
	case commandMidnight:
		if s.consumeTimer(midnightTimerID, command.generation) {
			err = s.reconcileOwned(s.config)
			if err != nil {
				// The callback consumed the only midnight timer. Keep the
				// event-driven horizon alive even when this reconciliation
				// attempt fails.
				if retryErr := s.scheduleMidnight(s.config); retryErr != nil {
					err = errors.Join(err, retryErr)
				}
			}
		}
	}
	if err != nil && command.result == nil {
		s.reportError(err)
	}
	if command.result != nil {
		command.result <- err
	}
}

func (s *Scheduler) enqueue(kind schedulerCommandKind, id string, generation uint64) {
	s.lifecycleMu.Lock()
	if !s.started || s.stopping || s.stopped {
		s.lifecycleMu.Unlock()
		return
	}
	commands, done := s.commands, s.done
	s.lifecycleMu.Unlock()
	command := schedulerCommand{kind: kind, id: id, generation: generation}
	select {
	case commands <- command:
	case <-done:
	}
}

func (s *Scheduler) reconcileOwned(config domain.Config) error {
	if s.states == nil {
		return errors.New("runtime state repository is required")
	}
	now := s.clock.Now().UTC()
	if !config.Enabled {
		if err := s.states.Update(func(state *domain.RuntimeState) error {
			ensureRuntimeStateMaps(state)
			for id := range state.NextRuns {
				delete(state.NextRuns, id)
			}
			for id, occurrence := range state.Occurrences {
				if occurrence.Status == domain.OccurrenceFailed && !occurrence.CompensationAttempted && !occurrence.CompensationDueAt.IsZero() {
					markMissed(&occurrence, "scheduler_disabled")
					state.Occurrences[id] = occurrence
				}
			}
			return nil
		}); err != nil {
			return err
		}
		s.cancelOccurrenceTimers()
		return s.scheduleMidnight(config)
	}
	if s.planner == nil {
		return errors.New("planner is required")
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	localNow := now.In(location)
	plans := make([]domain.PlannedOccurrence, 0)
	for offset := 0; offset <= schedulerHorizonDays; offset++ {
		date := time.Date(localNow.Year(), localNow.Month(), localNow.Day()+offset, 12, 0, 0, 0, location)
		dayPlans, planErr := s.planner.PlanDay(config, date)
		if planErr != nil {
			return planErr
		}
		plans = append(plans, dayPlans...)
	}

	var resulting domain.RuntimeState
	err = s.states.Update(func(state *domain.RuntimeState) error {
		ensureRuntimeStateMaps(state)
		desired := make(map[string]struct{}, len(plans))
		for _, plan := range plans {
			id := strings.TrimSpace(plan.ID)
			if id == "" {
				continue
			}
			plan.ID = id
			plan.AccountKey = strings.TrimSpace(plan.AccountKey)
			plan.WindowStart = plan.WindowStart.UTC()
			plan.WindowEnd = plan.WindowEnd.UTC()
			plan.PlannedAt = plan.PlannedAt.UTC()
			desired[id] = struct{}{}
			if existing, exists := state.Occurrences[id]; exists {
				if existing.Status == domain.OccurrencePlanned {
					// A stable identity represents the same logical slot, not an
					// immutable instant. Refresh the planned payload so a config
					// change moves both persistence and timer ownership together.
					existing.PlannedOccurrence = plan
					delete(state.NextRuns, id)
					if plan.Missed {
						markMissed(&existing, plan.MissedReason)
					} else if plan.PlannedAt.IsZero() || !plan.PlannedAt.After(now) {
						markMissed(&existing, "past_due")
					} else {
						existing.Status = domain.OccurrencePlanned
						state.NextRuns[id] = plan.PlannedAt.UTC()
					}
					state.Occurrences[id] = existing
				} else if existing.Status == domain.OccurrenceFailed && !existing.CompensationAttempted && !existing.CompensationDueAt.IsZero() {
					state.NextRuns[id] = existing.CompensationDueAt.UTC()
				}
				continue
			}
			entry := domain.OccurrenceState{PlannedOccurrence: plan}
			switch {
			case plan.Missed:
				markMissed(&entry, plan.MissedReason)
			case plan.PlannedAt.IsZero() || !plan.PlannedAt.After(now):
				markMissed(&entry, "past_due")
			default:
				entry.Status = domain.OccurrencePlanned
				state.NextRuns[id] = plan.PlannedAt
			}
			state.Occurrences[id] = entry
		}

		// Existing planned occurrences not in the current horizon or no longer
		// selected remain history, but lose execution ownership. If the same
		// schedule is selected again later, its stable ID can be reconciled.
		for id, occurrence := range state.Occurrences {
			if _, exists := desired[id]; exists {
				continue
			}
			switch occurrence.Status {
			case domain.OccurrencePlanned:
				delete(state.NextRuns, id)
			case domain.OccurrenceFailed:
				if !occurrence.CompensationAttempted && !occurrence.CompensationDueAt.IsZero() {
					markMissed(&occurrence, "selection_removed")
					state.Occurrences[id] = occurrence
				}
			}
		}
		for id := range state.NextRuns {
			occurrence, exists := state.Occurrences[id]
			if !exists || (occurrence.Status != domain.OccurrencePlanned &&
				!(occurrence.Status == domain.OccurrenceFailed && !occurrence.CompensationAttempted && !occurrence.CompensationDueAt.IsZero())) {
				delete(state.NextRuns, id)
			}
		}
		resulting = cloneRuntimeState(*state)
		return nil
	})
	if err != nil {
		return err
	}
	s.reconcileTimers(resulting, now)
	return s.scheduleMidnight(config)
}

func (s *Scheduler) reconcileTimers(state domain.RuntimeState, now time.Time) {
	active := make(map[string]time.Time)
	for id, occurrence := range state.Occurrences {
		switch occurrence.Status {
		case domain.OccurrencePlanned:
			if occurrence.PlannedAt.IsZero() || !occurrence.PlannedAt.After(now) {
				continue
			}
			plannedAt := occurrence.PlannedAt.UTC()
			active[id] = plannedAt
			if due, exists := s.timerDue[id]; !exists || !due.Equal(plannedAt) {
				s.replaceTimer(id, plannedAt, now, func(generation uint64) {
					s.enqueue(commandOccurrence, id, generation)
				})
			}
		case domain.OccurrenceFailed:
			if occurrence.CompensationAttempted || occurrence.CompensationDueAt.IsZero() {
				continue
			}
			key := compensationTimerID(id)
			due := occurrence.CompensationDueAt.UTC()
			active[key] = due
			if scheduled, exists := s.timerDue[key]; !exists || !scheduled.Equal(due) {
				s.replaceTimer(key, due, now, func(generation uint64) {
					s.enqueue(commandCompensation, id, generation)
				})
			}
		}
	}
	for id, timer := range s.timers {
		if id == midnightTimerID {
			continue
		}
		due, exists := active[id]
		if !exists || !s.timerDue[id].Equal(due) {
			s.stopTimer(id, timer)
		}
	}
}

func (s *Scheduler) replaceTimer(id string, due, now time.Time, callback func(uint64)) {
	if timer, exists := s.timers[id]; exists {
		timer.Stop()
		delete(s.timers, id)
		delete(s.timerDue, id)
	}
	s.timerGeneration[id]++
	generation := s.timerGeneration[id]
	due = due.UTC()
	delay := due.Sub(now.UTC())
	if delay < 0 {
		delay = 0
	}
	s.timerDue[id] = due
	s.timers[id] = s.clock.AfterFunc(delay, func() {
		callback(generation)
	})
}

func (s *Scheduler) stopTimer(id string, timer domain.Timer) {
	if timer != nil {
		timer.Stop()
	}
	delete(s.timers, id)
	delete(s.timerDue, id)
	s.timerGeneration[id]++
}

func (s *Scheduler) consumeTimer(id string, generation uint64) bool {
	if generation != 0 && s.timerGeneration[id] != generation {
		return false
	}
	timer, exists := s.timers[id]
	if !exists {
		return generation == 0
	}
	s.stopTimer(id, timer)
	return true
}

func (s *Scheduler) retryTimer(id string, kind schedulerCommandKind, now time.Time) {
	if s.isStopped() {
		return
	}
	due := now.UTC().Add(schedulerRetryDelay)
	s.replaceTimer(id, due, now, func(generation uint64) {
		callbackID := id
		if kind == commandCompensation {
			callbackID = strings.TrimPrefix(id, compensationPrefix)
		}
		s.enqueue(kind, callbackID, generation)
	})
}

func (s *Scheduler) handleOccurrence(id string, generation uint64) error {
	if !s.consumeTimer(id, generation) || s.states == nil || s.exec == nil {
		return nil
	}
	now := s.clock.Now().UTC()
	var occurrence domain.PlannedOccurrence
	var claimed bool
	err := s.states.Update(func(state *domain.RuntimeState) error {
		ensureRuntimeStateMaps(state)
		entry, exists := state.Occurrences[id]
		next, owned := state.NextRuns[id]
		if !exists || entry.Status != domain.OccurrencePlanned || entry.PlannedAt.IsZero() || entry.PlannedAt.After(now) ||
			!owned || !next.Equal(entry.PlannedAt) || !s.config.Enabled || !scheduledForConfig(s.config, entry.AccountKey) {
			return nil
		}
		entry.Status = domain.OccurrenceRunning
		state.Occurrences[id] = entry
		delete(state.NextRuns, id)
		occurrence = entry.PlannedOccurrence
		claimed = true
		return nil
	})
	if err != nil {
		s.retryTimer(id, commandOccurrence, now)
		return fmt.Errorf("claim occurrence %q: %w", id, err)
	}
	if !claimed {
		return nil
	}
	record, executing := s.execute(func() domain.OperationRecord {
		return s.exec.ExecutePreheat(s.ctx, occurrence)
	})
	if !executing {
		return nil
	}
	status := statusForRecord(record)
	terminalErr := s.states.Update(func(state *domain.RuntimeState) error {
		ensureRuntimeStateMaps(state)
		entry, exists := state.Occurrences[id]
		if !exists {
			return nil
		}
		entry.Status = status
		if status != domain.OccurrenceFailed {
			entry.CompensationDueAt = time.Time{}
			entry.CompensationAttempted = false
		}
		if status == domain.OccurrenceFailed && !entry.CompensationAttempted && !entry.CompensationDueAt.IsZero() {
			state.NextRuns[id] = entry.CompensationDueAt.UTC()
		} else {
			delete(state.NextRuns, id)
		}
		state.Occurrences[id] = entry
		return nil
	})
	if terminalErr != nil {
		terminalErr = fmt.Errorf("save terminal occurrence %q: %w", id, terminalErr)
	}
	// Re-read the latest config and advance the horizon after every callback.
	reconcileErr := s.reconcileOwned(s.config)
	if reconcileErr != nil {
		reconcileErr = fmt.Errorf("reconcile after occurrence %q: %w", id, reconcileErr)
	}
	return errors.Join(terminalErr, reconcileErr)
}

func (s *Scheduler) handleCompensation(id string, generation uint64) error {
	timerID := compensationTimerID(id)
	if !s.consumeTimer(timerID, generation) || s.states == nil || s.exec == nil {
		return nil
	}
	state, err := s.states.Load()
	if err != nil {
		s.retryTimer(timerID, commandCompensation, s.clock.Now().UTC())
		return fmt.Errorf("load compensation occurrence %q: %w", id, err)
	}
	entry, exists := state.Occurrences[id]
	next, owned := state.NextRuns[id]
	if !exists || entry.Status != domain.OccurrenceFailed || entry.CompensationAttempted || entry.CompensationDueAt.IsZero() ||
		!owned || !next.Equal(entry.CompensationDueAt) || !s.config.Enabled || !scheduledForConfig(s.config, entry.AccountKey) {
		return nil
	}
	occurrence := entry.PlannedOccurrence
	eligible := true
	if guard, ok := s.exec.(CompensationEligibility); ok {
		// Eligibility may perform host and repository I/O. It must run outside
		// RuntimeStateRepository.Update's transaction to avoid lock inversion.
		eligible = guard.CompensationEligible(s.ctx, occurrence)
	}
	claimed := false
	err = s.states.Update(func(state *domain.RuntimeState) error {
		ensureRuntimeStateMaps(state)
		entry, exists := state.Occurrences[id]
		next, owned := state.NextRuns[id]
		if !exists || entry.Status != domain.OccurrenceFailed || entry.CompensationAttempted || entry.CompensationDueAt.IsZero() ||
			!owned || !next.Equal(entry.CompensationDueAt) || !s.config.Enabled || !scheduledForConfig(s.config, entry.AccountKey) {
			return nil
		}
		if _, held := state.GuardrailHolds[strings.TrimSpace(entry.AccountKey)]; held {
			eligible = false
		}
		entry.CompensationAttempted = true
		entry.CompensationDueAt = time.Time{}
		delete(state.NextRuns, id)
		if !eligible {
			entry.Status = domain.OccurrenceSkipped
		} else {
			entry.Status = domain.OccurrenceRunning
			claimed = true
		}
		state.Occurrences[id] = entry
		return nil
	})
	if err != nil {
		s.retryTimer(timerID, commandCompensation, s.clock.Now().UTC())
		return fmt.Errorf("claim compensation occurrence %q: %w", id, err)
	}
	if !claimed && eligible {
		return nil
	}
	if !eligible {
		return s.reconcileOwned(s.config)
	}
	record, executing := s.execute(func() domain.OperationRecord {
		if compensation, ok := s.exec.(CompensationExecutor); ok {
			return compensation.ExecuteCompensation(s.ctx, occurrence)
		}
		return s.exec.ExecutePreheat(s.ctx, occurrence)
	})
	if !executing {
		return nil
	}
	// The compensation is one-shot. The executor may have observed another
	// transient failure and written a new due time; clear it unconditionally.
	terminalErr := s.states.Update(func(state *domain.RuntimeState) error {
		ensureRuntimeStateMaps(state)
		entry, exists := state.Occurrences[id]
		if !exists {
			return nil
		}
		entry.Status = statusForRecord(record)
		entry.CompensationDueAt = time.Time{}
		entry.CompensationAttempted = true
		delete(state.NextRuns, id)
		state.Occurrences[id] = entry
		return nil
	})
	if terminalErr != nil {
		terminalErr = fmt.Errorf("save compensation occurrence %q: %w", id, terminalErr)
	}
	reconcileErr := s.reconcileOwned(s.config)
	if reconcileErr != nil {
		reconcileErr = fmt.Errorf("reconcile after compensation %q: %w", id, reconcileErr)
	}
	return errors.Join(terminalErr, reconcileErr)
}

func (s *Scheduler) scheduleMidnight(config domain.Config) error {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	now := s.clock.Now().UTC().In(location)
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, location).UTC()
	if due, exists := s.timerDue[midnightTimerID]; exists && due.Equal(next) {
		return nil
	}
	s.replaceTimer(midnightTimerID, next, s.clock.Now().UTC(), func(generation uint64) {
		// The callback is consumed by the owner; a subsequent reconcile creates
		// the next local-midnight timer.
		s.enqueue(commandMidnight, "", generation)
	})
	return nil
}

func (s *Scheduler) stopTimers() {
	for id, timer := range s.timers {
		s.stopTimer(id, timer)
	}
}

func (s *Scheduler) cancelOccurrenceTimers() {
	for id, timer := range s.timers {
		if id == midnightTimerID {
			continue
		}
		s.stopTimer(id, timer)
	}
}

func (s *Scheduler) isStopped() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return !s.started || s.stopping || s.stopped
}

func (s *Scheduler) execute(operation func() domain.OperationRecord) (domain.OperationRecord, bool) {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.isStopped() {
		return domain.OperationRecord{}, false
	}
	return operation(), true
}

func (s *Scheduler) reportError(err error) {
	if err == nil || s.exec == nil {
		return
	}
	if observer, ok := s.exec.(schedulerErrorObserver); ok {
		observer.ReportSchedulerError(err)
	}
}

func statusForRecord(record domain.OperationRecord) domain.OccurrenceStatus {
	switch record.RequestOutcome {
	case domain.RequestSucceeded:
		return domain.OccurrenceSucceeded
	case domain.RequestDisabled:
		return domain.OccurrenceSkipped
	default:
		return domain.OccurrenceFailed
	}
}

func compensationTimerID(id string) string { return compensationPrefix + id }

func scheduledForConfig(config domain.Config, accountKey string) bool {
	accountKey = strings.TrimSpace(accountKey)
	for _, key := range config.ScheduledAccountKeys {
		if strings.TrimSpace(key) == accountKey {
			return true
		}
	}
	return false
}

func markMissed(state *domain.OccurrenceState, reason string) {
	state.Status = domain.OccurrenceMissed
	state.PlannedOccurrence.Missed = true
	if strings.TrimSpace(reason) == "" {
		reason = "restart"
	}
	state.PlannedOccurrence.MissedReason = reason
	state.CompensationDueAt = time.Time{}
	state.CompensationAttempted = false
}

func ensureRuntimeStateMaps(state *domain.RuntimeState) {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Occurrences == nil {
		state.Occurrences = make(map[string]domain.OccurrenceState)
	}
	if state.NextRuns == nil {
		state.NextRuns = make(map[string]time.Time)
	}
	if state.GuardrailHolds == nil {
		state.GuardrailHolds = make(map[string]domain.GuardrailHold)
	}
}

func cloneRuntimeState(state domain.RuntimeState) domain.RuntimeState {
	copy := state
	copy.Occurrences = make(map[string]domain.OccurrenceState, len(state.Occurrences))
	for key, value := range state.Occurrences {
		copy.Occurrences[key] = value
	}
	copy.NextRuns = make(map[string]time.Time, len(state.NextRuns))
	for key, value := range state.NextRuns {
		copy.NextRuns[key] = value.UTC()
	}
	copy.GuardrailHolds = make(map[string]domain.GuardrailHold, len(state.GuardrailHolds))
	for key, value := range state.GuardrailHolds {
		copy.GuardrailHolds[key] = value
	}
	return copy
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

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

func (wallClock) AfterFunc(delay time.Duration, fn func()) domain.Timer {
	return wallTimer{timer: time.AfterFunc(delay, fn)}
}

type wallTimer struct{ timer *time.Timer }

func (t wallTimer) Stop() bool {
	if t.timer == nil {
		return false
	}
	return t.timer.Stop()
}
