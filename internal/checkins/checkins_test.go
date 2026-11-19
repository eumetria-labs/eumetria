package checkins

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
)

var ctx = context.Background()

func n(v int) *int { return &v }

func TestClean(t *testing.T) {
	in, err := Input{Energy: n(4), Note: "  slept   badly ", Tags: []string{"travel", "sick", "alcohol"}, Dismissed: []string{"alcohol"}}.Clean()
	if err != nil {
		t.Fatal(err)
	}
	// The list's order; a tag set isn't also dismissed.
	if in.Note != "slept badly" || !slices.Equal(in.Tags, []string{"alcohol", "sick", "travel"}) || len(in.Dismissed) != 0 {
		t.Errorf("clean = %+v", in)
	}
	for _, bad := range []Input{
		{Energy: n(0)}, {Mood: n(6)}, {Tags: []string{"hangover"}}, {Dismissed: []string{"sick"}}, {Note: string(make([]rune, MaxNote+1))},
	} {
		if _, err := bad.Clean(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestStore(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u, err := auth.NewStore(d).CreateUser(ctx, "anna", "a long enough password", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: d, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	c, err := s.Put(ctx, "2026-11-18", Input{Energy: n(2), Soreness: n(4), Tags: []string{"sick"}, Dismissed: []string{"alcohol"}}, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *c.Energy != 2 || c.Stress != nil || !slices.Equal(c.Tags, []string{"sick"}) || !slices.Equal(c.Dismissed, []string{"alcohol"}) || c.UpdatedBy != "anna" {
		t.Errorf("put = %+v", c)
	}
	s.Put(ctx, "2026-11-19", Input{Mood: n(5), Tags: []string{"travel"}}, u.ID)
	s.Put(ctx, "2026-11-20", Input{Mood: n(3)}, u.ID)
	skip, _ := s.SkipDays(ctx)
	if !skip["2026-11-18"] || !skip["2026-11-19"] || skip["2026-11-20"] || len(skip) != 2 {
		t.Errorf("skip = %v", skip)
	}
	// Changing the day replaces its tags.
	s.Put(ctx, "2026-11-18", Input{Energy: n(3)}, u.ID)
	list, _ := s.List(ctx, "2026-11-01", "2026-11-30")
	if len(list) != 3 || len(list[0].Tags) != 0 || len(list[0].Dismissed) != 0 || *list[0].Energy != 3 {
		t.Errorf("list = %+v", list)
	}
	if ok, err := s.Delete(ctx, "2026-11-19"); !ok || err != nil {
		t.Errorf("delete = %v, %v", ok, err)
	}
	if ok, _ := s.Delete(ctx, "2026-11-19"); ok {
		t.Error("deleted twice")
	}
	if skip, _ := s.SkipDays(ctx); len(skip) != 0 {
		t.Errorf("skip after the changes = %v", skip)
	}
}
