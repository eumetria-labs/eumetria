package demo

// A Withings scale for the last four months: a weigh-in most mornings with
// the weight Yazio has that day (Yazio copies the scale's), and today's,
// and the body composition around it, body fat slowly going down.

import (
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"

	"eumetria/internal/store"
)

func addBody(h *store.History, today time.Time) {
	r := rand.New(rand.NewPCG(31, 37)) // its own, so the rest stays as it was
	dates := make([]string, 0, len(h.Days))
	for date := range h.Days {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	from := today.AddDate(0, 0, -120).Format(time.DateOnly)
	h.Body = map[string]*store.Body{}
	n := 0
	last := 0.0
	end := today.Format(time.DateOnly)
	for _, date := range dates {
		d := h.Days[date]
		if d.Weight != nil {
			last = *d.Weight
		}
		// Today always: the Today card shows the morning's weigh-in.
		if date < from || d.Weight == nil && (date != end || last == 0) {
			continue
		}
		day, _ := time.Parse(time.DateOnly, date)
		w := last
		// From about 20.5 % to 18.5 % over the four months.
		fat := 20.5 - 2*float64(n)/100 + (r.Float64()-0.5)*0.8
		n++
		fatKg := w * fat / 100
		lean := w - fatKg
		round := func(v float64, d int) float64 { p := math.Pow10(d); return math.Round(v*p) / p }
		at := time.Date(day.Year(), day.Month(), day.Day(), 6, 50+r.IntN(25), 0, 0, time.UTC)
		id := strconv.Itoa(900000 + n)
		h.Body[id] = &store.Body{GrpID: id, Date: date, Time: at, TZ: "Europe/Berlin", Device: "demo-scale", Modified: at.Unix(),
			Measures: map[string]float64{
				"weight": round(w, 2), "fat_ratio": round(fat, 1), "fat_mass": round(fatKg, 2), "fat_free_mass": round(lean, 2),
				"muscle_mass": round(lean*0.94, 2), "hydration": round(lean*0.72, 2), "bone_mass": round(3.1+r.Float64()*0.2, 2),
				"visceral_fat": float64(7 + r.IntN(2)),
			}}
	}
	h.BodyUpdate = today.Unix()
}
