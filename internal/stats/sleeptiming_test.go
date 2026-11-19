package stats

import (
	"encoding/json"
	"os"
	"testing"
)

// The cases tests/sleeptiming.test.js checks for the page.
func TestSleepTimingLikeThePage(t *testing.T) {
	b, err := os.ReadFile("../../tests/fixtures/sleeptiming.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name     string
			Days     []Day
			Travel   []string
			FreeDays []int `json:"free_days"`
			Holidays []string
			Off      []string
			Want     struct {
				Nights, Pairs   int
				OnsetSD, WakeSD *int
				SRI             *int
				Chrono          struct {
					OK                      bool
					Free, Work              int
					MSF, MSW, MSFsc, Jetlag int
				}
			}
		}
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	eq := func(a, b *int) bool { return b == nil || a != nil && *a == *b }
	for _, c := range f.Cases {
		r := SleepRegularity(c.Days)
		w := c.Want
		if r.Nights != w.Nights || r.Pairs != w.Pairs || !eq(r.OnsetSD, w.OnsetSD) || !eq(r.WakeSD, w.WakeSD) || !eq(r.SRI, w.SRI) {
			t.Errorf("%s: regularity = %+v, want %+v", c.Name, r, w)
		}
		skip := map[string]bool{}
		for _, d := range c.Travel {
			skip[d] = true
		}
		hol, off := map[string]string{}, map[string]bool{}
		for _, d := range c.Holidays {
			hol[d] = "a holiday"
		}
		for _, d := range c.Off {
			off[d] = true
		}
		ch := SleepChronotype(c.Days, skip, FreeNightOf(Output{FreeDays: c.FreeDays, Holidays: hol}, off))
		wc := w.Chrono
		if ch.OK != wc.OK || ch.FreeNights != wc.Free || ch.WorkNights != wc.Work ||
			wc.OK && (ch.msf != wc.MSF || ch.msw != wc.MSW || ch.msfsc != wc.MSFsc || *ch.JetlagMin != wc.Jetlag) {
			t.Errorf("%s: chronotype = %+v, want %+v", c.Name, ch, wc)
		}
	}
}

func TestSleepTimingClock(t *testing.T) {
	if clockOf(988) != "04:28" || clockOf(0) != "12:00" || clockOf(720) != "00:00" {
		t.Error(clockOf(988), clockOf(0), clockOf(720))
	}
}
