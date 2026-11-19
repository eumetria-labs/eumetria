// Package holidays knows the public holidays of Germany (nationwide and per
// state) and Austria, computed from rules (Easter by the Gregorian computus),
// so nothing is fetched. The chronotype counts the night before a holiday
// as a free night (stats/sleeptiming.go).
//
// Only holidays that hold in a whole state are listed: Mariä Himmelfahrt in
// Bavaria (only in Catholic communities), Fronleichnam in parts of Saxony
// and Thuringia, and Augsburg's Friedensfest are left out.
package holidays

import (
	"slices"
	"time"
)

// Region is a place with its own holidays.
type Region struct {
	Code string `json:"code"` // "DE", "DE-BY", "AT"
	Name string `json:"name"` // in its own language: "Bayern"
}

// Regions are the ones known, Germany's states after the country.
var Regions = []Region{
	{"DE", "Deutschland (bundesweit)"},
	{"DE-BW", "Baden-Württemberg"}, {"DE-BY", "Bayern"}, {"DE-BE", "Berlin"}, {"DE-BB", "Brandenburg"},
	{"DE-HB", "Bremen"}, {"DE-HH", "Hamburg"}, {"DE-HE", "Hessen"}, {"DE-MV", "Mecklenburg-Vorpommern"},
	{"DE-NI", "Niedersachsen"}, {"DE-NW", "Nordrhein-Westfalen"}, {"DE-RP", "Rheinland-Pfalz"}, {"DE-SL", "Saarland"},
	{"DE-SN", "Sachsen"}, {"DE-ST", "Sachsen-Anhalt"}, {"DE-SH", "Schleswig-Holstein"}, {"DE-TH", "Thüringen"},
	{"AT", "Österreich"},
}

// Known reports whether code is a region here ("" is none).
func Known(code string) bool {
	return code == "" || slices.ContainsFunc(Regions, func(r Region) bool { return r.Code == code })
}

// Easter is Easter Sunday of year (anonymous Gregorian algorithm).
func Easter(year int) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// a rule: a holiday's day in a year, the regions it holds in (nil: the
// whole country of the prefix) and the first year it does.
type rule struct {
	name    string
	country string
	day     func(year int) time.Time
	in      []string
	since   int
}

func fixed(month time.Month, day int) func(int) time.Time {
	return func(y int) time.Time { return time.Date(y, month, day, 0, 0, 0, 0, time.UTC) }
}

func easter(days int) func(int) time.Time {
	return func(y int) time.Time { return Easter(y).AddDate(0, 0, days) }
}

// repentance is Buß- und Bettag: the Wednesday before 23 November.
func repentance(y int) time.Time {
	d := time.Date(y, time.November, 22, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != time.Wednesday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

var rules = []rule{
	// Germany, nationwide.
	{"Neujahr", "DE", fixed(time.January, 1), nil, 0},
	{"Karfreitag", "DE", easter(-2), nil, 0},
	{"Ostermontag", "DE", easter(1), nil, 0},
	{"Tag der Arbeit", "DE", fixed(time.May, 1), nil, 0},
	{"Christi Himmelfahrt", "DE", easter(39), nil, 0},
	{"Pfingstmontag", "DE", easter(50), nil, 0},
	{"Tag der Deutschen Einheit", "DE", fixed(time.October, 3), nil, 0},
	{"1. Weihnachtstag", "DE", fixed(time.December, 25), nil, 0},
	{"2. Weihnachtstag", "DE", fixed(time.December, 26), nil, 0},
	// Germany, by state.
	{"Heilige Drei Könige", "DE", fixed(time.January, 6), []string{"DE-BW", "DE-BY", "DE-ST"}, 0},
	{"Internationaler Frauentag", "DE", fixed(time.March, 8), []string{"DE-BE"}, 2019},
	{"Internationaler Frauentag", "DE", fixed(time.March, 8), []string{"DE-MV"}, 2023},
	{"Ostersonntag", "DE", easter(0), []string{"DE-BB"}, 0},
	{"Pfingstsonntag", "DE", easter(49), []string{"DE-BB"}, 0},
	{"Fronleichnam", "DE", easter(60), []string{"DE-BW", "DE-BY", "DE-HE", "DE-NW", "DE-RP", "DE-SL"}, 0},
	{"Mariä Himmelfahrt", "DE", fixed(time.August, 15), []string{"DE-SL"}, 0},
	{"Weltkindertag", "DE", fixed(time.September, 20), []string{"DE-TH"}, 2019},
	{"Reformationstag", "DE", fixed(time.October, 31), []string{"DE-BB", "DE-MV", "DE-SN", "DE-ST", "DE-TH"}, 0},
	{"Reformationstag", "DE", fixed(time.October, 31), []string{"DE-HB", "DE-HH", "DE-NI", "DE-SH"}, 2018},
	{"Allerheiligen", "DE", fixed(time.November, 1), []string{"DE-BW", "DE-BY", "DE-NW", "DE-RP", "DE-SL"}, 0},
	{"Buß- und Bettag", "DE", repentance, []string{"DE-SN"}, 0},
	// Austria.
	{"Neujahr", "AT", fixed(time.January, 1), nil, 0},
	{"Heilige Drei Könige", "AT", fixed(time.January, 6), nil, 0},
	{"Ostermontag", "AT", easter(1), nil, 0},
	{"Staatsfeiertag", "AT", fixed(time.May, 1), nil, 0},
	{"Christi Himmelfahrt", "AT", easter(39), nil, 0},
	{"Pfingstmontag", "AT", easter(50), nil, 0},
	{"Fronleichnam", "AT", easter(60), nil, 0},
	{"Mariä Himmelfahrt", "AT", fixed(time.August, 15), nil, 0},
	{"Nationalfeiertag", "AT", fixed(time.October, 26), nil, 0},
	{"Allerheiligen", "AT", fixed(time.November, 1), nil, 0},
	{"Mariä Empfängnis", "AT", fixed(time.December, 8), nil, 0},
	{"Christtag", "AT", fixed(time.December, 25), nil, 0},
	{"Stefanitag", "AT", fixed(time.December, 26), nil, 0},
}

// Between returns the holidays of region from from to to (both included),
// date ("YYYY-MM-DD") to name; nil for no region.
func Between(region string, from, to time.Time) map[string]string {
	if region == "" || !Known(region) {
		return nil
	}
	country := region
	if len(region) > 2 {
		country = region[:2]
	}
	out := map[string]string{}
	for y := from.Year(); y <= to.Year(); y++ {
		for _, r := range rules {
			if r.country != country || y < r.since || r.in != nil && !slices.Contains(r.in, region) {
				continue
			}
			d := r.day(y)
			if d.Before(from) || d.After(to) {
				continue
			}
			out[d.Format(time.DateOnly)] = r.name
		}
	}
	return out
}
