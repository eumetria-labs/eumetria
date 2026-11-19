package stats

import (
	"sort"
	"time"

	"eumetria/internal/store"
)

// Body is a weigh-in's body composition: kg, % and the visceral fat index,
// each 0 when the scale didn't measure it.
type Body struct {
	Time     string  `json:"time,omitempty"` // local "HH:MM"
	FatPct   float64 `json:"fat_pct,omitempty"`
	FatKg    float64 `json:"fat_kg,omitempty"`
	LeanKg   float64 `json:"lean_kg,omitempty"`
	MuscleKg float64 `json:"muscle_kg,omitempty"`
	WaterKg  float64 `json:"water_kg,omitempty"`
	BoneKg   float64 `json:"bone_kg,omitempty"`
	Visceral float64 `json:"visceral,omitempty"`
}

// firstWeighIns is each day's first weigh-in with a weight.
func firstWeighIns(all map[string]*store.Body) map[string]*store.Body {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the same pick whatever the map's order
	out := map[string]*store.Body{}
	for _, k := range keys {
		b := all[k]
		if _, ok := b.Measure("weight"); !ok {
			continue
		}
		if cur := out[b.Date]; cur == nil || b.Time.Before(cur.Time) {
			out[b.Date] = b
		}
	}
	return out
}

func bodyOf(b *store.Body) *Body {
	m := func(k string, digits int) float64 {
		v, _ := b.Measure(k)
		return round(v, digits)
	}
	out := &Body{FatPct: m("fat_ratio", 1), FatKg: m("fat_mass", 1), LeanKg: m("fat_free_mass", 1), MuscleKg: m("muscle_mass", 1),
		WaterKg: m("hydration", 1), BoneKg: m("bone_mass", 1), Visceral: m("visceral_fat", 0)}
	if out.LeanKg == 0 && out.FatKg > 0 {
		w, _ := b.Measure("weight")
		fat, _ := b.Measure("fat_mass")
		out.LeanKg = round(w-fat, 1)
	}
	if *out == (Body{}) {
		return nil
	}
	if !b.Time.IsZero() {
		out.Time = clock(b.Time, 0)
		if loc, err := time.LoadLocation(b.TZ); b.TZ != "" && err == nil {
			out.Time = b.Time.In(loc).Format("15:04")
		}
	}
	return out
}
