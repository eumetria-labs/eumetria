package phases

import (
	"errors"
	"testing"
	"time"
)

func TestClean(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		in   Input
		ok   bool
		want Input
	}{
		{Input{Kind: Cut, Label: "  Winter   cut ", Start: "2026-10-01"}, true, Input{Kind: Cut, Label: "Winter cut", Start: "2026-10-01"}},
		{Input{Kind: Bulk, Start: "2026-10-01", End: "2026-11-30"}, true, Input{Kind: Bulk, Start: "2026-10-01", End: "2026-11-30"}},
		{Input{Kind: Maintain, Start: "2026-11-17"}, true, Input{Kind: Maintain, Start: "2026-11-17"}},
		// An event lies on one day, also ahead.
		{Input{Kind: Event, Label: "Open 26.1", Start: "2027-02-26", End: "2027-03-01"}, true, Input{Kind: Event, Label: "Open 26.1", Start: "2027-02-26", End: "2027-02-26"}},
		{Input{Kind: Event, Start: "2026-10-01"}, false, Input{}},              // needs a label
		{Input{Kind: Other, Start: "2026-10-01"}, false, Input{}},              // likewise
		{Input{Kind: "diet", Label: "x", Start: "2026-10-01"}, false, Input{}}, // unknown kind
		{Input{Kind: Cut, Start: "2026-11-20"}, false, Input{}},                // a phase can't start ahead
		{Input{Kind: Cut, Start: "2026-10-01", End: "2026-09-30"}, false, Input{}},
		{Input{Kind: Cut, Start: "2020-01-01", End: "2023-01-01"}, false, Input{}}, // over two years
		{Input{Kind: Cut, Start: "01.10.2026"}, false, Input{}},
		{Input{Kind: Event, Label: "far", Start: "2029-01-01"}, false, Input{}},
		{Input{Kind: Cut, Label: "a\x00b", Start: "2026-10-01"}, false, Input{}},
		// A vacation may lie ahead, and needs an end.
		{Input{Kind: Vacation, Start: "2027-07-01", End: "2027-07-14"}, true, Input{Kind: Vacation, Start: "2027-07-01", End: "2027-07-14"}},
		{Input{Kind: Vacation, Start: "2027-07-01"}, false, Input{}},
	} {
		got, err := c.in.Clean(now)
		if c.ok != (err == nil) {
			t.Errorf("%+v: err = %v", c.in, err)
			continue
		}
		if !c.ok {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%+v: err = %v, want ErrInvalid", c.in, err)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%+v = %+v, want %+v", c.in, got, c.want)
		}
	}
}
