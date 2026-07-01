package models_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
)

func TestScheduleIsActiveNowFailOpen(t *testing.T) {
	// Disabled schedule, or enabled with no windows: always active.
	s := models.ScheduleConfig{}
	if !s.IsActiveNow() {
		t.Errorf("disabled schedule should be active (fail-open)")
	}
	s2 := models.ScheduleConfig{Enabled: true}
	if !s2.IsActiveNow() {
		t.Errorf("enabled schedule with no windows should be active (fail-open)")
	}
}

func hhmm(t time.Time) string {
	return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
}

func TestScheduleIsActiveNowMatchesCurrentWindow(t *testing.T) {
	now := time.Now()
	weekday := (int(now.Weekday()) + 6) % 7 // Monday=0..Sunday=6

	s := models.ScheduleConfig{
		Enabled: true,
		ActiveWindows: []models.TimeWindow{
			{
				Days:  []int{weekday},
				Start: hhmm(now.Add(-time.Minute)),
				End:   hhmm(now.Add(time.Minute)),
			},
		},
	}
	if !s.IsActiveNow() {
		t.Errorf("expected active: current time falls inside the configured window")
	}
}

func TestScheduleIsActiveNowWrongDay(t *testing.T) {
	now := time.Now()
	weekday := (int(now.Weekday()) + 6) % 7
	wrongDay := (weekday + 1) % 7

	s := models.ScheduleConfig{
		Enabled: true,
		ActiveWindows: []models.TimeWindow{
			{Days: []int{wrongDay}, Start: "00:00", End: "23:59"},
		},
	}
	if s.IsActiveNow() {
		t.Errorf("expected inactive: window is for a different weekday")
	}
}

func TestScheduleIsActiveNowOutsideTimeRange(t *testing.T) {
	now := time.Now()
	weekday := (int(now.Weekday()) + 6) % 7

	s := models.ScheduleConfig{
		Enabled: true,
		ActiveWindows: []models.TimeWindow{
			{
				Days:  []int{weekday},
				Start: hhmm(now.Add(5 * time.Minute)),
				End:   hhmm(now.Add(10 * time.Minute)),
			},
		},
	}
	if s.IsActiveNow() {
		t.Errorf("expected inactive: current time is before the configured window")
	}
}

func TestTimeWindowNormalizeDayWrap(t *testing.T) {
	w := models.TimeWindow{Days: []int{-1, 7, 8}}
	w.Normalize()
	want := []int{6, 0, 1}
	if len(w.Days) != len(want) {
		t.Fatalf("Days = %v, want %v", w.Days, want)
	}
	for i, d := range want {
		if w.Days[i] != d {
			t.Errorf("Days[%d] = %d, want %d", i, w.Days[i], d)
		}
	}
}
