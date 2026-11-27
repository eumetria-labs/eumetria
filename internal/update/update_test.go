package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	if v, ok := ParseVersion("v1.12.3"); !ok || v != (Version{1, 12, 3}) {
		t.Fatalf("v1.12.3: %v %v", v, ok)
	}
	for _, bad := range []string{"1.2", "1.2.3-rc1", "x1.2.3", ""} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if !(Version{1, 9, 9}).Less(Version{1, 10, 0}) || (Version{2, 0, 0}).Less(Version{1, 99, 99}) {
		t.Error("Less")
	}
	cases := map[string]Build{
		"1.0.0":        {Kind: "release", Version: Version{1, 0, 0}},
		"main-3ba126d": {Kind: "edge", Commit: "3ba126d"},
		"dev":          {Kind: "dev"},
		"main-XYZ1234": {Kind: "dev"},
	}
	for in, want := range cases {
		if got := ParseBuild(in); got != want {
			t.Errorf("%s: %+v, want %+v", in, got, want)
		}
	}
}

// fake is GitHub: the releases, the green runs and the comparisons.
func fake(t *testing.T, releases []ghRelease, green string, compare map[string]ghCompare) (*Checker, *[]string) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("User-Agent") != "eumetria-update-check" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		switch {
		case r.URL.Path == "/repos/"+Repo+"/releases":
			json.NewEncoder(w).Encode(releases)
		case r.URL.Path == "/repos/"+Repo+"/actions/workflows/ci.yml/runs":
			if r.URL.Query().Get("status") != "success" || r.URL.Query().Get("branch") != "main" {
				t.Errorf("runs query %s", r.URL.RawQuery)
			}
			runs := map[string]any{"workflow_runs": []map[string]string{}}
			if green != "" {
				runs["workflow_runs"] = []map[string]string{{"head_sha": green}}
			}
			json.NewEncoder(w).Encode(runs)
		case strings.HasPrefix(r.URL.Path, "/repos/"+Repo+"/compare/"):
			c, ok := compare[strings.TrimPrefix(r.URL.Path, "/repos/"+Repo+"/compare/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(c)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Checker{Base: srv.URL, Now: func() time.Time { return time.Date(2026, 12, 1, 4, 0, 0, 0, time.UTC) }}, &asked
}

func rel(tag string) ghRelease {
	return ghRelease{TagName: tag, Name: "Eumetria " + strings.TrimPrefix(tag, "v"), Body: "notes " + tag, HTMLURL: "https://example/" + tag}
}

func TestStable(t *testing.T) {
	draft := rel("v1.3.0")
	draft.Draft = true
	c, _ := fake(t, []ghRelease{rel("v1.0.0"), rel("v1.2.0"), rel("v1.1.0"), rel("v2.0.0"), rel("v2.1.0"), draft, rel("nightly")}, "", nil)
	ctx := context.Background()

	r, err := c.Check(ctx, Stable, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Available || r.Target != "1.2.0" || len(r.Releases) != 2 || r.Releases[1].Version != "1.1.0" {
		t.Fatalf("1.0.0: %+v", r)
	}
	if r.Major == nil || r.Major.Version != "2.1.0" {
		t.Fatalf("major: %+v", r.Major)
	}

	// The newest of the major: nothing to install, still the notice.
	if r, _ = c.Check(ctx, Stable, "1.2.0"); r.Available || r.Major == nil {
		t.Fatalf("1.2.0: %+v", r)
	}
	if r, _ = c.Check(ctx, Stable, "2.1.0"); r.Available || r.Major != nil {
		t.Fatalf("2.1.0: %+v", r)
	}
	// An edge build switching to stable: the newest release.
	if r, _ = c.Check(ctx, Stable, "main-abcdef1"); !r.Available || r.Target != "2.1.0" {
		t.Fatalf("edge on stable: %+v", r)
	}
	// A development build is never offered anything.
	if r, _ = c.Check(ctx, Stable, "dev"); r.Available || r.Target != "2.1.0" {
		t.Fatalf("dev: %+v", r)
	}
}

func TestEdge(t *testing.T) {
	green := "cccccccdddddddeeeeeee0000000111111122222"
	commit := func(sha, msg string) (c struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}) {
		c.SHA, c.HTMLURL, c.Commit.Message = sha, "https://example/"+sha, msg
		return c
	}
	ahead := ghCompare{Status: "ahead", AheadBy: 2}
	ahead.Commits = append(ahead.Commits, commit("aaaaaaa1111", "First\n\nbody"), commit(green, "Second"))
	c, asked := fake(t, nil, green, map[string]ghCompare{
		"abcdef1..." + green: ahead,
		"v1.0.0..." + green:  {Status: "identical"},
	})
	ctx := context.Background()

	r, err := c.Check(ctx, Edge, "main-abcdef1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Available || r.Target != "main-ccccccc" || r.Ahead != 2 || len(r.Commits) != 2 || r.Commits[0].Subject != "Second" || r.Commits[1].Subject != "First" || r.Commits[1].SHA != "aaaaaaa" {
		t.Fatalf("edge: %+v", r)
	}
	// Running the green commit: nothing, and no comparison asked.
	n := len(*asked)
	if r, _ = c.Check(ctx, Edge, "main-ccccccc"); r.Available {
		t.Fatalf("same: %+v", r)
	}
	if len(*asked) != n+1 {
		t.Fatalf("asked %v", (*asked)[n:])
	}
	// A release that main hasn't moved past.
	if r, _ = c.Check(ctx, Edge, "1.0.0"); r.Available {
		t.Fatalf("release: %+v", r)
	}
	if r, _ = c.Check(ctx, Edge, "dev"); r.Available || r.Target != "main-ccccccc" {
		t.Fatalf("dev: %+v", r)
	}
}

func TestRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := (&Checker{Base: srv.URL}).Check(context.Background(), Stable, "1.0.0")
	if err != ErrRateLimit {
		t.Fatalf("err %v", err)
	}
}
