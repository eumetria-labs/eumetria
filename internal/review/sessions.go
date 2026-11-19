package review

import (
	"fmt"
	"sort"
	"strings"

	"eumetria/internal/sessions"
)

// TrainingSession is a session noted set by set: per lift its work sets
// (runs of equal sets as "5×5 @ 100 kg") and the volume (kg × reps of the
// work sets; warm-ups left out).
type TrainingSession struct {
	Date  string        `json:"date"`
	Title string        `json:"title,omitempty"`
	RPE   *int          `json:"session_rpe,omitempty"`
	Lifts []SessionLift `json:"lifts"`
	Note  string        `json:"note,omitempty"`
}

type SessionLift struct {
	Lift     string  `json:"lift"`
	WorkSets string  `json:"work_sets"`
	VolumeKg float64 `json:"volume_kg,omitempty"`
	Warmups  int     `json:"warmup_sets,omitempty"`
}

// fmtSets writes work sets compactly, as the page does (sessions.js).
func fmtSets(sets []sessions.Set) string {
	type run struct {
		reps, n int
		kg      float64
	}
	var runs []run
	for _, s := range sets {
		if n := len(runs); n > 0 && runs[n-1].reps == s.Reps && runs[n-1].kg == s.Kg {
			runs[n-1].n++
			continue
		}
		runs = append(runs, run{s.Reps, 1, s.Kg})
	}
	parts := make([]string, len(runs))
	for i, r := range runs {
		w := "bodyweight"
		if r.kg > 0 {
			w = fmt.Sprintf("%g kg", r.kg)
		}
		if r.n > 1 {
			parts[i] = fmt.Sprintf("%d×%d @ %s", r.n, r.reps, w)
		} else {
			parts[i] = fmt.Sprintf("%d @ %s", r.reps, w)
		}
	}
	return strings.Join(parts, ", ")
}

// AddSessions puts the week's training sessions on it, the oldest first.
func (w *Week) AddSessions(all []sessions.Session) {
	last := w.To
	if len(w.Days) > 0 {
		last = w.Days[len(w.Days)-1].Date
	}
	for _, x := range all {
		if x.Date < w.From || x.Date > last {
			continue
		}
		ts := TrainingSession{Date: x.Date, Title: clip(x.Title, nameMax), RPE: x.RPE, Note: clip(x.Note, noteMax)}
		var order []string
		by := map[string][]sessions.Set{}
		warm := map[string]int{}
		for _, s := range x.Sets {
			if _, ok := by[s.Lift]; !ok {
				order = append(order, s.Lift)
				by[s.Lift] = nil
			}
			if s.Type == sessions.Warmup {
				warm[s.Lift]++
				continue
			}
			by[s.Lift] = append(by[s.Lift], s)
		}
		vol := x.Volumes()
		for _, lift := range order {
			ts.Lifts = append(ts.Lifts, SessionLift{Lift: lift, WorkSets: fmtSets(by[lift]), VolumeKg: round(vol[lift].Kg), Warmups: warm[lift]})
		}
		w.Sessions = append(w.Sessions, ts)
	}
	sort.SliceStable(w.Sessions, func(i, j int) bool { return w.Sessions[i].Date < w.Sessions[j].Date })
}
