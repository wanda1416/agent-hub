package quota

import (
	"testing"
	"time"
)

func TestCalculateRateWithinResetWindow(t *testing.T) {
	reset := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	firstObserved := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	lastObserved := firstObserved.Add(time.Hour)
	samples := []historySample{
		{UsedPercent: 20, ObservedAt: firstObserved, ResetAt: &reset},
		{UsedPercent: 30, ObservedAt: lastObserved, ResetAt: &reset},
	}
	used := 30.0
	pool := Pool{UsedPercent: &used, ObservedAt: &lastObserved, ResetAt: &reset}
	rate, exhaustion := calculateRate(samples, pool)
	if rate == nil || *rate != 10 {
		t.Fatalf("expected 10 percentage points/hour, got %v", rate)
	}
	if exhaustion == nil || !exhaustion.Equal(lastObserved.Add(7*time.Hour)) {
		t.Fatalf("unexpected exhaustion estimate %v", exhaustion)
	}
}

func TestCalculateRateRequiresFiveMinutesAndSameReset(t *testing.T) {
	reset := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	later := observed.Add(2 * time.Minute)
	used := 30.0
	pool := Pool{UsedPercent: &used, ObservedAt: &later, ResetAt: &reset}
	samples := []historySample{
		{UsedPercent: 20, ObservedAt: observed, ResetAt: &reset},
		{UsedPercent: 30, ObservedAt: later, ResetAt: &reset},
	}
	if rate, _ := calculateRate(samples, pool); rate != nil {
		t.Fatalf("interval below five minutes must not produce a rate: %v", *rate)
	}

	newReset := reset.Add(24 * time.Hour)
	pool.ResetAt = &newReset
	pool.ObservedAt = &later
	if rate, _ := calculateRate(samples, pool); rate != nil {
		t.Fatalf("samples from another reset window must not be used: %v", *rate)
	}
}

func TestCalculateRateDoesNotExceedReset(t *testing.T) {
	reset := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	later := observed.Add(time.Hour)
	samples := []historySample{
		{UsedPercent: 0, ObservedAt: observed, ResetAt: &reset},
		{UsedPercent: 10, ObservedAt: later, ResetAt: &reset},
	}
	used := 10.0
	pool := Pool{UsedPercent: &used, ObservedAt: &later, ResetAt: &reset}
	rate, exhaustion := calculateRate(samples, pool)
	if rate == nil {
		t.Fatal("positive rate should be retained")
	}
	if exhaustion != nil {
		t.Fatalf("estimate beyond reset must be omitted, got %v", exhaustion)
	}
}
