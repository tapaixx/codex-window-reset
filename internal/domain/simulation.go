package domain

import (
	"encoding/json"
	"time"
)

type TimelineSegment struct {
	Kind       string    `json:"kind"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	AccountKey string    `json:"account_key,omitempty"`
}

func (s TimelineSegment) MarshalJSON() ([]byte, error) {
	type wire TimelineSegment
	value := wire(s)
	value.Start = s.Start.UTC()
	value.End = s.End.UTC()
	return json.Marshal(value)
}

func (s *TimelineSegment) UnmarshalJSON(data []byte) error {
	type wire TimelineSegment
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = TimelineSegment(value)
	s.Start = s.Start.UTC()
	s.End = s.End.UTC()
	return nil
}

type StrategyMetrics struct {
	AvailableCoverageMinutes int `json:"available_coverage_minutes"`
	IdleWindowMinutes        int `json:"idle_window_minutes"`
}

type SimulationResult struct {
	WorkMinutes      int                 `json:"work_minutes"`
	Baseline         StrategyMetrics     `json:"baseline"`
	Scheduled        StrategyMetrics     `json:"scheduled"`
	NetGainMinutes   int                 `json:"net_gain_minutes"`
	PreheatWindows   []PlannedOccurrence `json:"preheat_windows"`
	TimelineSegments []TimelineSegment   `json:"timeline_segments"`
	Assumptions      map[string]int      `json:"assumptions"`
}

type StatusView struct {
	Enabled        bool                 `json:"enabled"`
	StoreErrorCode ErrorCode            `json:"store_error_code,omitempty"`
	NextRuns       map[string]time.Time `json:"next_runs"`
	RunID          string               `json:"run_id,omitempty"`
	RunTotal       int                  `json:"run_total"`
	RunCompleted   int                  `json:"run_completed"`
}

func (s StatusView) MarshalJSON() ([]byte, error) {
	type wire StatusView
	value := wire(s)
	value.NextRuns = utcTimes(s.NextRuns)
	return json.Marshal(value)
}

func (s *StatusView) UnmarshalJSON(data []byte) error {
	type wire StatusView
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = StatusView(value)
	s.NextRuns = utcTimes(s.NextRuns)
	return nil
}
