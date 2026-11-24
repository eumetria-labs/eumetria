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

func TestLayoutPromptTypes(t *testing.T) {
	list := func(v ...string) *[]string { return &v }
	if l, err := (Layout{}).Clean(); err != nil || l.PromptTypes != nil {
		t.Errorf("not set = %+v, %v", l, err)
	}
	// Empty is "none", not the default.
	if l, err := (Layout{PromptTypes: list()}).Clean(); err != nil || l.PromptTypes == nil || len(*l.PromptTypes) != 0 {
		t.Errorf("none = %+v, %v", l, err)
	}
	if l, err := (Layout{PromptTypes: list("CROSSFIT", "HIIT", "CROSSFIT")}).Clean(); err != nil || len(*l.PromptTypes) != 2 {
		t.Errorf("repeated = %+v, %v", l, err)
	}
	for _, bad := range []string{"", "crossfit", "WEIGHTS<b>", "A B"} {
		if _, err := (Layout{PromptTypes: list(bad)}).Clean(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	many := make([]string, 65)
	for i := range many {
		many[i] = "T" + string(rune('A'+i%26)) + string(rune('A'+i/26))
	}
	if _, err := (Layout{PromptTypes: &many}).Clean(); err == nil {
		t.Error("65 types accepted")
	}
}
