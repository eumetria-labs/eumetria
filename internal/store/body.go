package store

import "time"

// Body is one weigh-in on a Withings scale: the measures taken together,
// by name (withings.Types; an unknown code as "type_<code>"), in kg, % or
// the type's unit.
type Body struct {
	GrpID    string             `json:"grpid"`
	Date     string             `json:"date"` // the local day
	Time     time.Time          `json:"time"`
	TZ       string             `json:"tz,omitempty"`
	Device   string             `json:"device,omitempty"`
	Modified int64              `json:"modified"`
	Measures map[string]float64 `json:"measures"`
}

// Measure is one of its values, and whether it is there.
func (b *Body) Measure(key string) (float64, bool) {
	v, ok := b.Measures[key]
	return v, ok
}
