// Package update finds out whether a newer Eumetria is out (PLAN phase 15a).
// It asks GitHub's public API, without a token and without anything about
// the person: the releases for the stable channel, and for the edge channel
// the newest commit on main whose CI run passed (only those have an image).
//
// Applying an update is the updater container's job (phase 15b); this
// package only answers what there is and what changed.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Channels: stable follows the releases of one major version (":1"), edge
// every commit on main that passed CI (":latest").
const (
	Stable = "stable"
	Edge   = "edge"
)

// Repo is where the releases and the code are.
const Repo = "eumetria-labs/eumetria"

/* ---------- versions ---------- */

// Version is a release number, major.minor.patch.
type Version struct{ Major, Minor, Patch int }

var semver = regexp.MustCompile(`^v?(\d{1,4})\.(\d{1,4})\.(\d{1,6})$`)

// ParseVersion reads "1.2.3" or "v1.2.3".
func ParseVersion(s string) (Version, bool) {
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{a, b, c}, true
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v comes before o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// Build is what the running binary is: a release ("1.2.3"), an image from
// main ("main-3ba126d") or a development build ("dev", anything else).
type Build struct {
	Kind    string  // "release", "edge" or "dev"
	Version Version // for a release
	Commit  string  // for an edge build: the short commit
}

var edgeBuild = regexp.MustCompile(`^main-([0-9a-f]{7,40})$`)

// ParseBuild reads the version the binary was built with.
func ParseBuild(s string) Build {
	if v, ok := ParseVersion(s); ok {
		return Build{Kind: "release", Version: v}
	}
	if m := edgeBuild.FindStringSubmatch(s); m != nil {
		return Build{Kind: "edge", Commit: m[1]}
	}
	return Build{Kind: "dev"}
}

/* ---------- the answer ---------- */

// Release is one GitHub release.
type Release struct {
	Version   string    `json:"version"`
	Name      string    `json:"name"`
	Notes     string    `json:"notes"` // Markdown, the changelog section
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
}

// Commit is one commit on main.
type Commit struct {
	SHA     string    `json:"sha"` // short
	Subject string    `json:"subject"`
	Date    time.Time `json:"date"`
	URL     string    `json:"url"`
}

// Result is what a check found.
type Result struct {
	CheckedAt time.Time `json:"checked_at"`
	Channel   string    `json:"channel"`
	Running   string    `json:"running"`
	// Available: there is something newer to install on the channel. A
	// development build never has one: it is updated by hand.
	Available bool `json:"available"`
	// Target is what an update installs: a version, or "main-<commit>".
	Target string `json:"target,omitempty"`
	// Stable: the releases newer than the running one within its major
	// version, newest first; Major: the newest release of a higher major
	// version, which is never installed on its own.
	Releases []Release `json:"releases,omitempty"`
	Major    *Release  `json:"major,omitempty"`
	// Edge: the commits since the running one, newest first (at most
	// maxCommits of Ahead).
	Commits []Commit `json:"commits,omitempty"`
	Ahead   int      `json:"ahead,omitempty"`
}

const maxCommits = 50

/* ---------- asking GitHub ---------- */

// Checker asks GitHub. The zero value uses api.github.com.
type Checker struct {
	Client *http.Client
	Base   string // https://api.github.com without a trailing slash; tests use a fake
	Now    func() time.Time
}

// ErrRateLimit: GitHub's limit for requests without a token (60 an hour
// from one address) is used up.
var ErrRateLimit = errors.New("GitHub's rate limit is reached; try again later")

func (c *Checker) get(ctx context.Context, path string, query url.Values, into any) error {
	base := c.Base
	if base == "" {
		base = "https://api.github.com"
	}
	u := base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "eumetria-update-check")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body := io.LimitReader(res.Body, 4<<20)
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests {
		if res.Header.Get("X-RateLimit-Remaining") == "0" || res.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimit
		}
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s for %s", res.Status, path)
	}
	return json.NewDecoder(body).Decode(into)
}

// Check finds what is newer than running on channel.
func (c *Checker) Check(ctx context.Context, channel, running string) (Result, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	r := Result{CheckedAt: now().UTC(), Channel: channel, Running: running}
	b := ParseBuild(running)
	var err error
	switch channel {
	case Stable:
		err = c.stable(ctx, b, &r)
	case Edge:
		err = c.edge(ctx, b, &r)
	default:
		err = fmt.Errorf("unknown channel %q", channel)
	}
	if b.Kind == "dev" {
		r.Available = false
	}
	return r, err
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

type release struct {
	v Version
	r Release
}

// releases are the published releases, newest version first.
func (c *Checker) releases(ctx context.Context) ([]release, error) {
	var list []ghRelease
	if err := c.get(ctx, "/repos/"+Repo+"/releases", url.Values{"per_page": {"100"}}, &list); err != nil {
		return nil, err
	}
	var out []release
	for _, g := range list {
		v, ok := ParseVersion(g.TagName)
		if !ok || g.Draft || g.Prerelease {
			continue
		}
		out = append(out, release{v, Release{Version: v.String(), Name: g.Name, Notes: g.Body, URL: g.HTMLURL, Published: g.PublishedAt}})
	}
	slices.SortFunc(out, func(a, b release) int {
		if b.v.Less(a.v) {
			return -1
		}
		if a.v.Less(b.v) {
			return 1
		}
		return 0
	})
	return out, nil
}

// stable: the releases newer than the running one within its major
// version, and the newest of a higher major as a notice. An edge or
// development build on the stable channel is offered the newest release of
// the highest major (switching from edge: phase 15c decides whether that
// is safe for the database).
func (c *Checker) stable(ctx context.Context, b Build, r *Result) error {
	all, err := c.releases(ctx)
	if err != nil || len(all) == 0 {
		return err
	}
	if b.Kind != "release" {
		r.Target = all[0].r.Version
		r.Available = true
		r.Releases = []Release{all[0].r}
		return nil
	}
	for _, x := range all {
		switch {
		case x.v.Major > b.Version.Major:
			if r.Major == nil {
				rel := x.r
				r.Major = &rel
			}
		case x.v.Major == b.Version.Major && b.Version.Less(x.v):
			r.Releases = append(r.Releases, x.r)
		}
	}
	if len(r.Releases) > 0 {
		r.Available = true
		r.Target = r.Releases[0].Version
	}
	return nil
}

type ghRuns struct {
	WorkflowRuns []struct {
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_runs"`
}

type ghCompare struct {
	Status  string `json:"status"` // ahead, behind, identical, diverged
	AheadBy int    `json:"ahead_by"`
	Commits []struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	} `json:"commits"`
}

// edge: the newest commit on main whose ci.yml run passed (that run pushed
// its image), and the commits between the running build and it.
func (c *Checker) edge(ctx context.Context, b Build, r *Result) error {
	var runs ghRuns
	q := url.Values{"branch": {"main"}, "status": {"success"}, "exclude_pull_requests": {"true"}, "per_page": {"1"}}
	if err := c.get(ctx, "/repos/"+Repo+"/actions/workflows/ci.yml/runs", q, &runs); err != nil {
		return err
	}
	if len(runs.WorkflowRuns) == 0 || len(runs.WorkflowRuns[0].HeadSHA) < 7 {
		return nil
	}
	green := runs.WorkflowRuns[0].HeadSHA
	r.Target = "main-" + green[:7]
	var from string
	switch b.Kind {
	case "release":
		from = "v" + b.Version.String()
	case "edge":
		from = b.Commit
	default:
		return nil
	}
	if strings.HasPrefix(green, from) {
		return nil
	}
	var cmp ghCompare
	if err := c.get(ctx, "/repos/"+Repo+"/compare/"+url.PathEscape(from)+"..."+green, nil, &cmp); err != nil {
		return err
	}
	if cmp.Status != "ahead" && cmp.Status != "diverged" || cmp.AheadBy == 0 {
		return nil
	}
	r.Available, r.Ahead = true, cmp.AheadBy
	// GitHub lists them oldest first.
	for i := len(cmp.Commits) - 1; i >= 0 && len(r.Commits) < maxCommits; i-- {
		x := cmp.Commits[i]
		subject, _, _ := strings.Cut(x.Commit.Message, "\n")
		sha := x.SHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		r.Commits = append(r.Commits, Commit{SHA: sha, Subject: subject, Date: x.Commit.Author.Date, URL: x.HTMLURL})
	}
	return nil
}
