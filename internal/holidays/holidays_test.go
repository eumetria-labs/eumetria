package holidays

import (
	"testing"
	"time"
)

func TestEaster(t *testing.T) {
	for y, want := range map[int]string{2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05", 2027: "2027-03-28", 2038: "2038-04-25"} {
		if got := Easter(y).Format(time.DateOnly); got != want {
			t.Errorf("Easter %d = %s, want %s", y, got, want)
		}
	}
}

func TestBetween(t *testing.T) {
	year := func(y int) (time.Time, time.Time) {
		return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	from, to := year(2026)
	by := Between("DE-BY", from, to)
	// Bavaria 2026: 9 nationwide + Drei Könige, Fronleichnam, Allerheiligen.
	if len(by) != 12 || by["2026-06-04"] != "Fronleichnam" || by["2026-01-06"] == "" || by["2026-10-31"] != "" {
		t.Errorf("Bayern 2026 = %v", by)
	}
	sn := Between("DE-SN", from, to)
	if sn["2026-11-18"] != "Buß- und Bettag" || sn["2026-10-31"] == "" {
		t.Errorf("Sachsen 2026 = %v", sn)
	}
	if de := Between("DE", from, to); len(de) != 9 || de["2026-04-03"] != "Karfreitag" {
		t.Errorf("Deutschland 2026 = %v", de)
	}
	// Berlin's women's day since 2019 only.
	f18, t18 := year(2018)
	if Between("DE-BE", f18, t18)["2018-03-08"] != "" || Between("DE-BE", from, to)["2026-03-08"] == "" {
		t.Error("Frauentag in Berlin")
	}
	if at := Between("AT", from, to); len(at) != 13 || at["2026-10-26"] != "Nationalfeiertag" {
		t.Errorf("Österreich 2026 = %v", at)
	}
	if Between("", from, to) != nil || Between("XX", from, to) != nil || !Known("") || Known("DE-XX") {
		t.Error("no or an unknown region")
	}
}
