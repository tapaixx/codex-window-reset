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
	stopped     bool
	startErr    error
	commands    chan schedulerCommand
	stop        chan struct{}
	done        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc

	// timers is accessed only by the owner goroutine after Start. The
	// lifecycle mutex protects cleanup that can happen concurrently with Stop.
	timers map[string]domain.Timer
	config domain.Config
}

type schedulerCommand struct {
	kind   schedulerCommandKind
	id     string
	config domain.Config
	result chan error
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
		clock:   clock,
		planner: planner,
		states:  states,
		exec:    executor,
		timers:  make(map[string]domain.Timer),
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
	}

	go s.ownerLoop()
}

// Stop cancels timers and the owner goroutine. It is safe to call repeatedly.
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	if !s.started || s.stopped {
		done := s.done
		s.stopped = true
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	s.stopped = true
	stop := s.stop
	done := s.done
	cancel := s.cancel
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
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
	var err error
	switch command.kind {
	case commandReconcile:
		s.config = cloneConfig(command.config)
		err = s.reconcileOwned(command.config)
	case commandOccurrence:
		s.handleOccurrence(command.id)
	case commandCompensation:
		s.handleCompensation(command.id)
	case commandMidnight:
		if timer, exists := s.timers[midnightTimerID]; exists {
			timer.Stop()
			delete(s.timers, midnightTimerID)
		}
		err = s.reconcileOwned(s.config)
	}
	if command.result != nil {
		command.result <- err
	}
}

func (s *Scheduler) enqueue(kind schedulerCommandKind, id string) {
	s.lifecycleMu.Lock()
	if !s.started || s.stopped {
		s.lifecycleMu.Unlock()
		return
	}
	commands, done := s.commands, s.done
	s.lifecycleMu.Unlock()
	command := schedulerCommand{kind: kind, id: id}
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
		s.scheduleMidnight(config)
		return nil
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
					if existing.PlannedAt.IsZero() || !existing.PlannedAt.After(now) {
						markMissed(&existing, "past_due")
					} else {
						state.NextRuns[id] = existing.PlannedAt.UTC()
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
	s.scheduleMidnight(config)
	return nil
}

func (s *Scheduler) reconcileTimers(state domain.RuntimeState, now time.Time) {
	active := make(map[string]struct{})
	for id, occurrence := range state.Occurrences {
		switch occurrence.Status {
		case domain.OccurrencePlanned:
			if occurrence.PlannedAt.IsZero() || !occurrence.PlannedAt.After(now) {
				continue
			}
			active[id] = struct{}{}
			if _, exists := s.timers[id]; !exists {
				plannedAt := occurrence.PlannedAt
				s.timers[id] = s.clock.AfterFunc(plannedAt.Sub(now), func() {
					s.enqueue(commandOccurrence, id)
				})
			}
		case domain.OccurrenceFailed:
			if occurrence.CompensationAttempted || occurrence.CompensationDueAt.IsZero() {
				continue
			}
			key := compensationTimerID(id)
			active[key] = struct{}{}
			if _, exists := s.timers[key]; !exists {
				due := occurrence.CompensationDueAt
				delay := due.Sub(now)
				if delay < 0 {
					delay = 0
				}
				s.timers[key] = s.clock.AfterFunc(delay, func() {
					s.enqueue(commandCompensation, id)
				})
			}
		}
	}
	for id, timer := range s.timers {
		if id == midnightTimerID {
			continue
		}
		if _, exists := active[id]; !exists {
			timer.Stop()
			delete(s.timers, id)
		}
	}
}

func (s *Scheduler) handleOccurrence(id string) {
	if timer, exists := s.timers[id]; exists {
		timer.Stop()
		delete(s.timers, id)
	}
	if s.states == nil || s.exec == nil {
		return
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
	if err != nil || !claimed {
		return
	}
	record := s.exec.ExecutePreheat(s.ctx, occurrence)
	status := statusForRecord(record)
	_ = s.states.Update(func(state *domain.RuntimeState) error {
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
	// Re-read the latest config and advance the horizon after every callback.
	_ = s.reconcileOwned(s.config)
}

func (s *Scheduler) handleCompensation(id string) {
	timerID := compensationTimerID(id)
	if timer, exists := s.timers[timerID]; exists {
		timer.Stop()
		delete(s.timers, timerID)
	}
	if s.states == nil || s.exec == nil {
		return
	}
	state, err := s.states.Load()
	if err != nil {
		return
	}
	entry, exists := state.Occurrences[id]
	next, owned := state.NextRuns[id]
	if !exists || entry.Status != domain.OccurrenceFailed || entry.CompensationAttempted || entry.CompensationDueAt.IsZero() ||
		!owned || !next.Equal(entry.CompensationDueAt) || !s.config.Enabled || !scheduledForConfig(s.config, entry.AccountKey) {
		return
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
	if err != nil || (!claimed && eligible) {
		return
	}
	if !eligible {
		_ = s.reconcileOwned(s.config)
		return
	}
	var record domain.OperationRecord
	if compensation, ok := s.exec.(CompensationExecutor); ok {
		record = compensation.ExecuteCompensation(s.ctx, occurrence)
	} else {
		record = s.exec.ExecutePreheat(s.ctx, occurrence)
	}
	// The compensation is one-shot. The executor may have observed another
	// transient failure and written a new due time; clear it unconditionally.
	_ = s.states.Update(func(state *domain.RuntimeState) error {
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
	_ = s.reconcileOwned(s.config)
}

func (s *Scheduler) scheduleMidnight(config domain.Config) {
	if _, exists := s.timers[midnightTimerID]; exists {
		return
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return
	}
	now := s.clock.Now().UTC().In(location)
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, location).UTC()
	delay := next.Sub(s.clock.Now().UTC())
	if delay < 0 {
		delay = 0
	}
	s.timers[midnightTimerID] = s.clock.AfterFunc(delay, func() {
		// The callback is consumed by the owner; a subsequent reconcile creates
		// the next local-midnight timer.
		s.enqueue(commandMidnight, "")
	})
}

func (s *Scheduler) stopTimers() {
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
}

func (s *Scheduler) cancelOccurrenceTimers() {
	for id, timer := range s.timers {
		if id == midnightTimerID {
			continue
		}
		timer.Stop()
		delete(s.timers, id)
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
