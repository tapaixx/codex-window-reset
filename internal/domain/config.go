package domain

import (
	"fmt"
	"strings"
	"time"
)

type Config struct {
	SchemaVersion               int           `json:"schema_version"`
	Revision                    int64         `json:"revision"`
	Enabled                     bool          `json:"enabled"`
	Timezone                    string        `json:"timezone"`
	Weekdays                    []int         `json:"weekdays"`
	WorkPeriods                 []LocalPeriod `json:"work_periods"`
	PreheatLeadMinutes          *int          `json:"preheat_lead_minutes"`
	PreheatSpanMinutes          *int          `json:"preheat_span_minutes"`
	ProductivityMinutes         int           `json:"productivity_minutes"`
	WindowHours                 int           `json:"window_hours"`
	HealthThresholdPercent      int           `json:"health_threshold_percent"`
	SkipWindowTimes             []string      `json:"skip_window_times"`
	RemainingQuotaFloorPercent  int           `json:"remaining_quota_floor_percent"`
	RemainingWindowFloorMinutes int           `json:"remaining_window_floor_minutes"`
	LongWindowFloorPercent      int           `json:"long_window_floor_percent"`
	BlackoutPeriods             []LocalPeriod `json:"blackout_periods"`
	ProbeModel                  string        `json:"probe_model"`
	ProbeTimeoutSeconds         int           `json:"probe_timeout_seconds"`
	ScheduledAccountKeys        []string      `json:"scheduled_account_keys"`
}

type LocalPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type ValidationMode uint8

const (
	ValidatePersisted ValidationMode = iota
	ValidateSimulation
)

func DefaultConfig() Config {
	return Config{
		SchemaVersion:               1,
		Revision:                    1,
		Timezone:                    "Asia/Shanghai",
		Weekdays:                    []int{1, 2, 3, 4, 5},
		WorkPeriods:                 []LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "13:30", End: "19:00"}},
		ProductivityMinutes:         60,
		WindowHours:                 5,
		HealthThresholdPercent:      80,
		SkipWindowTimes:             []string{},
		RemainingQuotaFloorPercent:  20,
		RemainingWindowFloorMinutes: 60,
		LongWindowFloorPercent:      10,
		ProbeModel:                  "gpt-5.6-luna",
		ProbeTimeoutSeconds:         30,
		ScheduledAccountKeys:        []string{},
		BlackoutPeriods:             []LocalPeriod{},
	}
}

// ValidateConfig checks both persisted schedules and simulation drafts. The
// only activation rule is conditional on Enabled, so a disabled draft may
// intentionally omit account and preheat activation fields.
func ValidateConfig(cfg Config, mode ValidationMode, knownAccounts map[string]struct{}) error {
	invalid := func(format string, args ...any) error {
		return &Error{
			Code:       CodeConfigInvalid,
			Message:    fmt.Sprintf(format, args...),
			HTTPStatus: 400,
		}
	}

	if mode != ValidatePersisted && mode != ValidateSimulation {
		return invalid("unsupported validation mode")
	}
	if cfg.Timezone == "Local" {
		return invalid("timezone must be a valid IANA location")
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return invalid("timezone must be a valid IANA location")
	}

	seenWeekdays := make(map[int]struct{}, len(cfg.Weekdays))
	for _, weekday := range cfg.Weekdays {
		if weekday < 1 || weekday > 7 {
			return invalid("weekday must be between 1 and 7")
		}
		if _, ok := seenWeekdays[weekday]; ok {
			return invalid("weekdays must be unique")
		}
		seenWeekdays[weekday] = struct{}{}
	}

	if err := validatePeriods("work period", cfg.WorkPeriods); err != nil {
		return invalid("%s", err)
	}
	if err := validatePeriods("blackout period", cfg.BlackoutPeriods); err != nil {
		return invalid("%s", err)
	}

	if cfg.ProductivityMinutes <= 0 {
		return invalid("productivity minutes must be positive")
	}
	if cfg.WindowHours < 1 || cfg.WindowHours > 24 {
		return invalid("window hours must be between 1 and 24")
	}
	if cfg.HealthThresholdPercent < 0 || cfg.HealthThresholdPercent > 100 {
		return invalid("health threshold percent must be between 0 and 100")
	}
	seenSkipTimes := make(map[string]struct{}, len(cfg.SkipWindowTimes))
	for _, value := range cfg.SkipWindowTimes {
		if _, err := clockMinutes(value); err != nil {
			return invalid("skip window times must use HH:MM")
		}
		if _, duplicate := seenSkipTimes[value]; duplicate {
			return invalid("skip window times must be unique")
		}
		seenSkipTimes[value] = struct{}{}
	}
	if cfg.RemainingWindowFloorMinutes <= 0 {
		return invalid("remaining window floor minutes must be positive")
	}
	if cfg.RemainingQuotaFloorPercent < 0 || cfg.RemainingQuotaFloorPercent > 100 {
		return invalid("remaining quota floor percent must be between 0 and 100")
	}
	if cfg.LongWindowFloorPercent < 0 || cfg.LongWindowFloorPercent > 100 {
		return invalid("long window floor percent must be between 0 and 100")
	}
	if cfg.ProbeTimeoutSeconds < 5 || cfg.ProbeTimeoutSeconds > 120 {
		return invalid("probe timeout seconds must be between 5 and 120")
	}
	if strings.TrimSpace(cfg.ProbeModel) == "" {
		return invalid("probe model must not be empty")
	}

	if (cfg.PreheatLeadMinutes == nil) != (cfg.PreheatSpanMinutes == nil) {
		return invalid("preheat lead and span must be supplied together")
	}
	if cfg.PreheatLeadMinutes != nil {
		if *cfg.PreheatLeadMinutes < 1 || *cfg.PreheatLeadMinutes > 1440 {
			return invalid("preheat lead minutes must be between 1 and 1440")
		}
		if *cfg.PreheatSpanMinutes < 1 || *cfg.PreheatSpanMinutes > 1440 {
			return invalid("preheat span minutes must be between 1 and 1440")
		}
		for _, period := range cfg.WorkPeriods {
			start, _ := clockMinutes(period.Start)
			if start < *cfg.PreheatLeadMinutes+*cfg.PreheatSpanMinutes {
				return invalid("derived preheat window must remain on the work period's local date")
			}
		}
	}

	seenAccounts := make(map[string]struct{}, len(cfg.ScheduledAccountKeys))
	for _, key := range cfg.ScheduledAccountKeys {
		if strings.TrimSpace(key) == "" {
			return invalid("scheduled account keys must not be empty")
		}
		if _, ok := seenAccounts[key]; ok {
			return invalid("scheduled account keys must be unique")
		}
		seenAccounts[key] = struct{}{}
		if _, ok := knownAccounts[key]; !ok {
			return invalid("scheduled account %q was not discovered", key)
		}
	}

	if cfg.Enabled {
		if len(cfg.WorkPeriods) == 0 {
			return invalid("at least one work period is required when scheduling is enabled")
		}
		if cfg.PreheatLeadMinutes == nil || cfg.PreheatSpanMinutes == nil {
			return invalid("preheat lead and span are required when scheduling is enabled")
		}
	}
	return nil
}

func validatePeriods(label string, periods []LocalPeriod) error {
	var previousStart, previousEnd int
	for index, period := range periods {
		start, err := clockMinutes(period.Start)
		if err != nil {
			return fmt.Errorf("%s %d has an invalid start clock", label, index)
		}
		end, err := clockMinutes(period.End)
		if err != nil {
			return fmt.Errorf("%s %d has an invalid end clock", label, index)
		}
		if start >= end {
			return fmt.Errorf("%s %d must end after it starts", label, index)
		}
		if index > 0 {
			if start < previousStart {
				return fmt.Errorf("%s periods must be sorted", label)
			}
			if start < previousEnd {
				return fmt.Errorf("%s periods must not overlap", label)
			}
		}
		previousStart, previousEnd = start, end
	}
	return nil
}

func clockMinutes(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return 0, fmt.Errorf("invalid clock")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}
