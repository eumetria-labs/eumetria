package auth

import "testing"

func TestLayoutRetestMonths(t *testing.T) {
	n := func(v int) *int { return &v }
	if l, err := (Layout{RetestMonths: n(9)}).Clean(); err != nil || *l.RetestMonths != 9 {
		t.Errorf("9 months = %+v, %v", l, err)
	}
	if l, err := (Layout{}).Clean(); err != nil || l.RetestMonths != nil {
		t.Errorf("not set = %+v, %v", l, err)
	}
	for _, bad := range []int{-1, 25} {
		if _, err := (Layout{RetestMonths: n(bad)}).Clean(); err == nil {
			t.Errorf("%d months accepted", bad)
		}
	}
}
