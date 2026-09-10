package domain

import "testing"

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", want)
	}
	got, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T (%v)", err, err)
	}
	if got.Code != want {
		t.Fatalf("expected error code %q, got %q", want, got.Code)
	}
}

func TestDefaultConfigStartsInert(t *testing.T) {
	got := DefaultConfig()
	if got.Enabled || len(got.ScheduledAccountKeys) != 0 || got.PreheatLeadMinutes != nil || got.PreheatSpanMinutes != nil {
		t.Fatalf("unsafe defaults: %#v", got)
	}
	if got.Timezone != "Asia/Shanghai" || got.ProbeModel != "gpt-5.6-luna" || got.ProbeTimeoutSeconds != 30 {
		t.Fatalf("unexpected defaults: %#v", got)
	}
}

func TestValidateConfigRequiresActivationFields(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	err := ValidateConfig(cfg, ValidatePersisted, map[string]struct{}{})
	assertCode(t, err, CodeConfigInvalid)
}

func TestValidateConfigRejectsCrossMidnightAndUnknownAccount(t *testing.T) {
	lead, span := 120, 60
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.WorkPeriods = []LocalPeriod{{Start: "23:00", End: "01:00"}}
	cfg.PreheatLeadMinutes, cfg.PreheatSpanMinutes = &lead, &span
	cfg.ScheduledAccountKeys = []string{"missing"}
	assertCode(t, ValidateConfig(cfg, ValidatePersisted, map[string]struct{}{"known": {}}), CodeConfigInvalid)
}

func TestValidateConfigRejectsMalformedAndUnorderedCalendarValues(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{name: "timezone", edit: func(cfg *Config) { cfg.Timezone = "Not/IANA" }},
		{name: "weekday out of range", edit: func(cfg *Config) { cfg.Weekdays = []int{1, 8} }},
		{name: "duplicate weekday", edit: func(cfg *Config) { cfg.Weekdays = []int{1, 1} }},
		{name: "clock not round trip", edit: func(cfg *Config) { cfg.WorkPeriods = []LocalPeriod{{Start: "9:00", End: "12:00"}} }},
		{name: "overlapping work periods", edit: func(cfg *Config) {
			cfg.WorkPeriods = []LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "11:00", End: "13:00"}}
		}},
		{name: "unsorted blackout periods", edit: func(cfg *Config) {
			cfg.BlackoutPeriods = []LocalPeriod{{Start: "13:00", End: "14:00"}, {Start: "09:00", End: "10:00"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.edit(&cfg)
			assertCode(t, ValidateConfig(cfg, ValidatePersisted, nil), CodeConfigInvalid)
		})
	}
}

func TestValidateConfigRejectsThresholdBoundariesAndPreheatCrossingDate(t *testing.T) {
	lead, span := 1, 1
	base := DefaultConfig()
	base.Enabled = true
	base.ScheduledAccountKeys = []string{"known"}
	base.PreheatLeadMinutes, base.PreheatSpanMinutes = &lead, &span

	cases := []struct {
		name string
		edit func(*Config)
	}{
		{name: "productivity zero", edit: func(cfg *Config) { cfg.ProductivityMinutes = 0 }},
		{name: "window floor zero", edit: func(cfg *Config) { cfg.RemainingWindowFloorMinutes = 0 }},
		{name: "quota floor above one hundred", edit: func(cfg *Config) { cfg.RemainingQuotaFloorPercent = 101 }},
		{name: "long floor below zero", edit: func(cfg *Config) { cfg.LongWindowFloorPercent = -1 }},
		{name: "probe timeout too short", edit: func(cfg *Config) { cfg.ProbeTimeoutSeconds = 4 }},
		{name: "probe timeout too long", edit: func(cfg *Config) { cfg.ProbeTimeoutSeconds = 121 }},
		{name: "preheat lead zero", edit: func(cfg *Config) { zero := 0; cfg.PreheatLeadMinutes = &zero }},
		{name: "preheat span too long", edit: func(cfg *Config) { tooLong := 1441; cfg.PreheatSpanMinutes = &tooLong }},
		{name: "preheat begins previous local date", edit: func(cfg *Config) {
			cfg.WorkPeriods = []LocalPeriod{{Start: "00:30", End: "01:30"}}
			large := 60
			cfg.PreheatLeadMinutes, cfg.PreheatSpanMinutes = &large, &large
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.edit(&cfg)
			assertCode(t, ValidateConfig(cfg, ValidatePersisted, map[string]struct{}{"known": {}}), CodeConfigInvalid)
		})
	}
}

func TestValidateConfigAllowsBoundaryThresholdsAndDisabledDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RemainingQuotaFloorPercent = 0
	cfg.LongWindowFloorPercent = 100
	if err := ValidateConfig(cfg, ValidatePersisted, nil); err != nil {
		t.Fatalf("valid disabled config rejected: %v", err)
	}
}

func TestValidateSimulationPermitsDisabledDraftWithCompleteValidation(t *testing.T) {
	cfg := DefaultConfig()
	if err := ValidateConfig(cfg, ValidateSimulation, nil); err != nil {
		t.Fatalf("disabled simulation draft rejected: %v", err)
	}
}

func TestValidateConfigRejectsPartialPreheatConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	lead := 60
	cfg.PreheatLeadMinutes = &lead
	assertCode(t, ValidateConfig(cfg, ValidatePersisted, nil), CodeConfigInvalid)
}

func TestValidateConfigRejectsUnknownAndDuplicateScheduledAccounts(t *testing.T) {
	known := map[string]struct{}{"known": {}}
	cases := []struct {
		name string
		keys []string
	}{
		{name: "unknown", keys: []string{"missing"}},
		{name: "empty", keys: []string{""}},
		{name: "duplicate", keys: []string{"known", "known"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.ScheduledAccountKeys = tt.keys
			assertCode(t, ValidateConfig(cfg, ValidatePersisted, known), CodeConfigInvalid)
		})
	}
}
