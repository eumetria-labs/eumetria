package review

import (
	"testing"
	"time"

	"eumetria/internal/checkins"
	"eumetria/internal/stats"
)

func TestWeekSleepTiming(t *testing.T) {
	monday := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	var out stats.Output
	for d := monday.AddDate(0, 0, -21); !d.After(monday.AddDate(0, 0, 6)); d = d.AddDate(0, 0, 1) {
		sl := &stats.Sleep{Start: "23:00", End: "06:30"}
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			sl = &stats.Sleep{Start: "00:30", End: "09:30"}
		}
		out.Days = append(out.Days, stats.Day{Date: d.Format(time.DateOnly), Sleep: sl})
	}
	w := BuildWeek(out, monday, "2026-11-20")
	st := w.SleepTiming
	if st == nil || st.Regularity.Nights != 7 || st.Regularity.SRI == nil || st.Chronotype == nil || *st.Chronotype.JetlagMin != 135 || st.Chronotype.MSFsc != "04:28" {
		t.Fatalf("sleep timing = %+v", st)
	}
	// Three weekends away leave too few free nights.
	var away []checkins.Checkin
	for _, d := range []string{"2026-10-24", "2026-10-31", "2026-11-07"} {
		away = append(away, checkins.Checkin{Date: d, Tags: []string{"travel"}})
	}
	w.AddCheckins(away)
	if w.SleepTiming.Chronotype != nil {
		t.Errorf("chronotype with three weekends away = %+v", w.SleepTiming.Chronotype)
	}
}
