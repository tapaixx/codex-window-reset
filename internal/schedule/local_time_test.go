package schedule

import (
	"testing"
	"time"
)

func TestResolveLocalMinuteChoosesEarliestFallbackInstant(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, time.November, 1, 0, 0, 0, 0, location)

	got, ok := resolveLocalMinute(location, date, 90)
	if !ok {
		t.Fatal("fallback local minute was not resolved")
	}
	want := time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("fallback minute = %s, want earliest instant %s", got, want)
	}
}

func TestResolveLocalMinuteRejectsSpringForwardGap(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, time.March, 8, 0, 0, 0, 0, location)

	if _, ok := resolveLocalMinute(location, date, 2*60+30); ok {
		t.Fatal("nonexistent spring-forward minute resolved")
	}
}
