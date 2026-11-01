package main

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/stats"
	"eumetria/internal/store"
)

// published is the data the server hands out: the synced history in
// memory, and the stats built from it once per set of scopes that asked,
// until the next sync replaces the history. Nothing of it is written to
// disk; photos stay in their one copy in the data folder.
type published struct {
	opts stats.Options // the settings; the parts come from the reader's scopes

	mu     sync.Mutex
	hist   *store.History
	at     time.Time // when hist was synced: the stats' generated_at
	builds map[string]*build
}

// build is the stats for one set of scopes, with the files they point to.
type build struct {
	stats    []byte            // the JSON of /api/stats
	etag     string            // its hash, for revalidation
	files    map[string][]byte // "routes/<hash>.json" and "days/<hash>.json"
	photos   map[string]bool   // the photo files these stats show
	workouts map[string]string // the workouts they show: ID → day
}

func newPublished(opts stats.Options) *published {
	return &published{opts: opts, hist: &store.History{}, builds: map[string]*build{}}
}

// set replaces the history after a sync.
func (p *published) set(h *store.History, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hist, p.at, p.builds = h, at, map[string]*build{}
}

// get returns the stats for the scopes a user's role (or token) gives,
// building them on first use. Admins also get the Anthropic credit status.
func (p *published) get(scopes []string, admin bool) (*build, error) {
	key := strings.Join(scopes, " ")
	if admin {
		key += " +status"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if b := p.builds[key]; b != nil {
		return b, nil
	}
	opts := p.opts
	opts.Nutrition = slices.Contains(scopes, auth.ViewNutrition)
	opts.Foods = slices.Contains(scopes, auth.ViewFoods)
	opts.Photos = slices.Contains(scopes, auth.ViewPhotos)
	opts.Activity = slices.Contains(scopes, auth.ViewActivity)
	opts.Health = slices.Contains(scopes, auth.ViewHealth)
	opts.Routes = slices.Contains(scopes, auth.ViewRoutes)
	opts.Review = slices.Contains(scopes, auth.ViewReview)
	opts.CreditStatus = admin
	out := stats.Build(p.hist, opts, p.at)
	out.Scopes = scopes
	b := &build{photos: map[string]bool{}, workouts: map[string]string{}}
	photo := func(src string) {
		if strings.HasPrefix(src, opts.PhotoPath) { // not the demo's data: URLs
			b.photos[path.Base(src)] = true
		}
	}
	// Every day, before splitFiles moves the older ones into their year files.
	for _, d := range out.Days {
		for _, ph := range d.Photos {
			photo(ph.Src)
		}
		for _, w := range d.Workouts {
			b.workouts[w.ID] = d.Date
		}
	}
	for _, r := range out.Recipes {
		photo(r.Image)
	}
	var err error
	if b.files, err = splitFiles(&out); err != nil {
		return nil, err
	}
	if b.stats, err = json.Marshal(out); err != nil {
		return nil, err
	}
	b.etag = contentTag(b.stats)
	p.builds[key] = b
	return b, nil
}
