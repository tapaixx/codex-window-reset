package domain

import "time"

type TimelineSegment struct {
	Kind       string    `json:"kind"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	AccountKey string    `json:"account_key,omitempty"`
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
