package demo

import (
	"encoding/json"
	"testing"
	"time"
)

// The sample data must depend only on the day it is made for: CI checks the
// page with it, and a failure there has to be repeatable with -demo -now.
func TestSameDaySameData(t *testing.T) {
	today := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	make := func() string {
		w, r := Wods(today)
		b, err := json.Marshal([]any{History(today, 120), Lifts(today), Phases(today), Checkins(today), w, r, Devices(today)})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := make()
	for range 5 {
		if make() != first {
			t.Fatal("the sample data differs between two runs for the same day")
		}
	}
}
