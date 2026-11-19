package review

import (
	"slices"

	"eumetria/internal/phases"
	"eumetria/internal/stats"
)

// GoalHit is how many of the week's days met a goal, as the page's Insights
// card counts them (site/js/correlate.js, goalHits): days without data
// don't count.
type GoalHit struct {
	Goal string `json:"goal"`
	Hit  int    `json:"days_met"`
	Of   int    `json:"days_counted"`
	Rule string `json:"rule,omitempty"`
}

// calorieBand is how far from the goal a day outside a cut or bulk may be.
const calorieBand = 0.1

// phaseKind is "cut" or "bulk" when date lies in one of those phases (the
// latest-starting one that covers it), else "maintain".
func phaseKind(all []phases.Phase, date string) string {
	kind := "maintain"
	start := ""
	for _, p := range all {
		if p.Kind == phases.Event || p.Kind == phases.Vacation || p.Start > date || (p.End != "" && p.End < date) || p.Start < start {
			continue
		}
		start = p.Start
		kind = p.Kind
	}
	if kind != phases.Cut && kind != phases.Bulk {
		return "maintain"
	}
	return kind
}

// goalHits counts the week's complete days against the goals.
func (w *Week) goalHits() {
	var protein, energy, steps, sleep, dry [2]int // met, counted
	kinds := map[string]bool{}
	count := func(c *[2]int, ok bool) {
		c[1]++
		if ok {
			c[0]++
		}
	}
	for _, d := range w.complete {
		if d.Energy > 0 {
			if d.ProteinGoal > 0 {
				goal := d.ProteinGoal
				if d.EnergyBase > 0 && d.EnergyGoal > 0 {
					goal *= d.EnergyGoal / d.EnergyBase
				}
				count(&protein, d.Protein >= goal)
			}
			goal := d.EnergyGoal
			if goal == 0 {
				goal = d.EnergyBase
			}
			if goal > 0 {
				kind := phaseKind(w.phaseList, d.Date)
				kinds[kind] = true
				switch kind {
				case phases.Cut:
					count(&energy, d.Energy <= goal)
				case phases.Bulk:
					count(&energy, d.Energy >= goal)
				default:
					count(&energy, d.Energy >= goal*(1-calorieBand) && d.Energy <= goal*(1+calorieBand))
				}
			}
			if w.alcoholKnown {
				count(&dry, d.Nutrients["alcohol"][0] == 0)
			}
		}
		if w.Goals.Steps > 0 && d.Steps > 0 {
			count(&steps, d.Steps >= w.Goals.Steps)
		}
		if w.sleepGoal > 0 && d.Sleep != nil && d.Sleep.Asleep > 0 {
			count(&sleep, d.Sleep.Asleep >= w.sleepGoal)
		}
	}
	rule := "within ±10 % of the goal (incl. the day's activity)"
	if kinds[phases.Cut] || kinds[phases.Bulk] {
		rule = "in a cut at or below the goal, in a bulk at or above it, otherwise within ±10 %"
	}
	w.GoalHits = nil
	for _, g := range []struct {
		name string
		c    [2]int
		rule string
	}{
		{"protein", protein, "at or above the goal"},
		{"calories", energy, rule},
		{"steps", steps, "at or above the goal"},
		{"sleep", sleep, "time asleep at or above the goal"},
		{"alcohol_free", dry, "no alcohol in the foods logged"},
	} {
		if g.c[1] > 0 {
			w.GoalHits = append(w.GoalHits, GoalHit{Goal: g.name, Hit: g.c[0], Of: g.c[1], Rule: g.rule})
		}
	}
}

// alcoholEver tells whether any day of out has alcohol data: without it,
// every day would count as alcohol-free.
func alcoholEver(out stats.Output) bool {
	return slices.ContainsFunc(out.Days, func(d stats.Day) bool { _, ok := d.Nutrients["alcohol"]; return ok })
}
