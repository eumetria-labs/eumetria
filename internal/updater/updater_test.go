package updater

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const prefix = "ghcr.io/eumetria-labs/eumetria"

// world is a fake Docker host with one compose project: the containers,
// the images by ref, what the registry has, and what compose was asked.
type world struct {
	mu         sync.Mutex
	t          *testing.T
	dir        string
	containers map[string]*Container // by name
	images     map[string]Image      // by ref and by ID
	registry   map[string]string     // ref → the image ID a pull gets
	bad        map[string]bool       // image IDs that never get healthy
	usesTag    bool                  // compose.yaml uses ${EUMETRIA_TAG}
	calls      [][]string
	tagged     []string
	handover   []string
}

func newWorld(t *testing.T) *world {
	w := &world{t: t, dir: t.TempDir(), containers: map[string]*Container{}, images: map[string]Image{}, registry: map[string]string{}, bad: map[string]bool{}, usesTag: true}
	add := func(service, name, repo, id, version string) {
		w.containers[name] = &Container{ID: "c-" + name, Name: name, Image: id, Ref: repo + ":1", State: "running",
			Labels: map[string]string{"com.docker.compose.project": "eumetria", "com.docker.compose.service": service}}
		w.image(repo+":1", id, version)
	}
	add("eumetria", "eumetria", prefix, "sha-app-1.0", "1.0.0")
	w.containers["eumetria"].Health = "healthy"
	add("claude-runner", "claude-runner", prefix+"-claude-runner", "sha-run-1.0", "1.0.0")
	add("updater", "eumetria-updater", prefix+"-updater", "sha-upd-1.0", "1.0.0")
	// A one-off run of Eumetria (-create-user): not a service.
	w.containers["eumetria-run-1"] = &Container{ID: "c-run", Name: "eumetria-run-1", Image: "sha-app-1.0", Ref: prefix + ":1", State: "running",
		Labels: map[string]string{"com.docker.compose.project": "eumetria", "com.docker.compose.service": "eumetria", "com.docker.compose.oneoff": "True"}}
	// Someone else's service in the same project: never touched.
	w.containers["nginx"] = &Container{ID: "c-nginx", Name: "nginx", Image: "sha-nginx", Ref: "nginx:1", State: "running",
		Labels: map[string]string{"com.docker.compose.project": "eumetria", "com.docker.compose.service": "nginx"}}
	return w
}

func (w *world) image(ref, id, version string) {
	img := Image{ID: id, Labels: map[string]string{"org.opencontainers.image.version": version}}
	w.images[ref], w.images[id] = img, img
}

func (w *world) updater() *Updater {
	return &Updater{Docker: w, Compose: w.compose, Project: "eumetria", Dir: w.dir, Prefix: prefix, Self: "c-eumetria-updater",
		State: w.dir, HealthWait: 200 * time.Millisecond, Poll: 5 * time.Millisecond}
}

func (w *world) Container(_ context.Context, id string) (Container, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.containers {
		if c.ID == id || c.Name == id {
			return *c, nil
		}
	}
	return Container{}, errors.New("no such container")
}

func (w *world) Project(_ context.Context, project string) ([]Container, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Container
	for _, c := range w.containers {
		if c.Labels["com.docker.compose.project"] == project {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (w *world) Image(_ context.Context, ref string) (Image, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	img, ok := w.images[ref]
	if !ok {
		return Image{}, errors.New("no such image " + ref)
	}
	return img, nil
}

func (w *world) Tag(_ context.Context, image, repo, tag string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.images[repo+":"+tag] = w.images[image]
	w.tagged = append(w.tagged, image+"→"+repo+":"+tag)
	return nil
}

func (w *world) RemoteDigest(_ context.Context, ref string) (string, error) {
	return "sha256:" + w.registry[ref], nil
}

func (w *world) HostName(context.Context) (string, error) { return "vps-1", nil }

func (w *world) Logs(context.Context, string, int) (string, error) { return "panic: boom", nil }

func (w *world) Handover(_ context.Context, image, from string, args []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handover = append([]string{image, from}, args...)
	return nil
}

// handedOver waits a moment for the handover, which comes after the job
// is recorded.
func (w *world) handedOver() []string {
	for range 100 {
		w.mu.Lock()
		h := w.handover
		w.mu.Unlock()
		if h != nil {
			return h
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// compose acts like docker compose on the fake host.
func (w *world) compose(_ context.Context, args ...string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, args)
	tag := (&Updater{Dir: w.dir}).Tag()
	ref := func(repo string) string {
		if w.usesTag {
			return repo + ":" + tag
		}
		return repo + ":1"
	}
	switch args[0] {
	case "config":
		return strings.Join([]string{ref(prefix), ref(prefix + "-claude-runner"), ref(prefix + "-updater"), "nginx:1"}, "\n"), nil
	case "pull":
		for _, svc := range args[1:] {
			if svc == "--quiet" {
				continue
			}
			for _, c := range w.containers {
				if c.Labels["com.docker.compose.service"] == svc {
					repo, _ := splitRef(c.Ref)
					if id, ok := w.registry[ref(repo)]; ok {
						w.images[ref(repo)] = w.images[id]
					}
				}
			}
		}
	case "up":
		for _, svc := range args[1:] {
			if strings.HasPrefix(svc, "--") {
				continue
			}
			for _, c := range w.containers {
				if c.Labels["com.docker.compose.service"] == svc {
					repo, _ := splitRef(c.Ref)
					c.Ref = ref(repo)
					c.Image = w.images[c.Ref].ID
					c.ID = "c-" + c.Name + "-" + c.Image
					if svc == "eumetria" {
						c.Health = map[bool]string{true: "unhealthy", false: "healthy"}[w.bad[c.Image]]
					}
				}
			}
		}
	}
	return "", nil
}

// release puts version on the registry under tag for all three images.
func (w *world) release(tag, version string) {
	for _, r := range []string{prefix, prefix + "-claude-runner", prefix + "-updater"} {
		id := "sha-" + filepath.Base(r) + "-" + version
		w.image(id, id, version)
		w.registry[r+":"+tag] = id
	}
}

func wait(t *testing.T, u *Updater) Job {
	t.Helper()
	for range 400 {
		if u.Current() == nil {
			if h := u.History(); len(h) > 0 {
				return h[0]
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the update didn't end")
	return Job{}
}

func TestCheckTag(t *testing.T) {
	for _, c := range []struct {
		from, to string
		err      error
	}{
		{"1", "1", nil}, {"1", "2", nil}, {"1", "latest", nil}, {"latest", "1", nil}, {"2", "latest", nil},
		{"2", "1", ErrDowngrade}, {"1", "1.2", ErrTag}, {"1", "1.2.3", ErrTag}, {"1", "0", ErrTag}, {"1", "edge", ErrTag}, {"1", "../1", ErrTag},
	} {
		if err := CheckTag(c.from, c.to); !errors.Is(err, c.err) {
			t.Errorf("%s → %s: %v, want %v", c.from, c.to, err, c.err)
		}
	}
}

func TestEnvKeepsOtherLines(t *testing.T) {
	u := &Updater{Dir: t.TempDir()}
	if u.Tag() != "1" {
		t.Fatal("default")
	}
	os.WriteFile(u.envFile(), []byte("COMPOSE_PROFILES=claude\nEUMETRIA_TAG='2'\nOTHER=x\n"), 0o644)
	if u.Tag() != "2" {
		t.Fatalf("tag %q", u.Tag())
	}
	if err := u.setTag("latest"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(u.envFile())
	if string(b) != "COMPOSE_PROFILES=claude\nEUMETRIA_TAG=latest\nOTHER=x\n" {
		t.Fatalf(".env = %q", b)
	}
	os.WriteFile(u.envFile(), []byte("EUMETRIA_TAG=1.2.3\n"), 0o644)
	if u.Tag() != "1" {
		t.Error("a pinned tag in .env reads as the default")
	}
}

func TestUpdate(t *testing.T) {
	w := newWorld(t)
	w.release("1", "1.1.0")
	u := w.updater()
	j, err := u.Start(context.Background(), "1", "eumetria-2026-12-01.tar.gz.enc")
	if err != nil || j.State != "running" {
		t.Fatalf("start: %+v %v", j, err)
	}
	done := wait(t, u)
	if done.State != "done" || done.Before["eumetria"] != "1.0.0" || done.After["eumetria"] != "1.1.0" || done.Backup == "" || !done.Handover {
		t.Fatalf("job: %+v", done)
	}
	// Only Eumetria's services, never nginx; the updater isn't recreated
	// by itself but by the helper.
	want := [][]string{{"config", "--images"}, {"pull", "--quiet", "claude-runner", "eumetria", "updater"}, {"up", "--detach", "--no-deps", "claude-runner", "eumetria"}}
	if !slices.EqualFunc(w.calls, want, slices.Equal) {
		t.Errorf("compose calls %q", w.calls)
	}
	if h := w.handedOver(); len(h) == 0 || h[0] != prefix+"-updater:1" || h[1] != "c-eumetria-updater" || !slices.Contains(h, "-handover") {
		t.Errorf("handover %q", h)
	}
	if w.containers["nginx"].Image != "sha-nginx" {
		t.Error("nginx was touched")
	}
}

func TestUpdateToEdgeWritesTheTag(t *testing.T) {
	w := newWorld(t)
	w.release("latest", "main-abc1234")
	u := w.updater()
	if _, err := u.Start(context.Background(), "latest", ""); err != nil {
		t.Fatal(err)
	}
	if j := wait(t, u); j.State != "done" || j.From != "1" || u.Tag() != "latest" {
		t.Fatalf("job %+v, tag %s", j, u.Tag())
	}
	// And back down to stable: allowed (the page guards the schema).
	if err := CheckTag(u.Tag(), "1"); err != nil {
		t.Error(err)
	}
}

func TestNothingNew(t *testing.T) {
	w := newWorld(t)
	u := w.updater()
	u.Start(context.Background(), "1", "")
	if j := wait(t, u); j.State != "unchanged" {
		t.Fatalf("job %+v", j)
	}
	if len(w.calls) != 2 {
		t.Errorf("calls %q", w.calls)
	}
}

func TestUnhealthyGoesBack(t *testing.T) {
	w := newWorld(t)
	w.release("latest", "main-bad0000")
	w.bad["sha-eumetria-main-bad0000"] = true
	u := w.updater()
	u.Start(context.Background(), "latest", "")
	j := wait(t, u)
	if j.State != "rolled_back" || !strings.Contains(j.Error, "unhealthy") || j.Log != "panic: boom" {
		t.Fatalf("job %+v", j)
	}
	if u.Tag() != "1" {
		t.Errorf("tag after going back: %s", u.Tag())
	}
	if c := w.containers["eumetria"]; c.Image != "sha-app-1.0" || c.Health != "healthy" {
		t.Errorf("eumetria runs %+v", c)
	}
	if !slices.Contains(w.tagged, "sha-app-1.0→"+prefix+":rollback") || !slices.Contains(w.tagged, prefix+":rollback→"+prefix+":1") || len(w.handover) != 0 {
		t.Errorf("tagged %q, handover %q", w.tagged, w.handover)
	}
}

func TestComposeWithoutTheVariable(t *testing.T) {
	w := newWorld(t)
	w.usesTag = false
	w.release("latest", "main-abc1234")
	u := w.updater()
	u.Start(context.Background(), "latest", "")
	j := wait(t, u)
	if j.State != "failed" || !strings.Contains(j.Error, "EUMETRIA_TAG") || u.Tag() != "1" {
		t.Fatalf("job %+v, tag %s", j, u.Tag())
	}
	if len(w.calls) != 1 {
		t.Errorf("went on after config: %q", w.calls)
	}
}

func TestRefused(t *testing.T) {
	w := newWorld(t)
	u := w.updater()
	os.WriteFile(filepath.Join(w.dir, ".env"), []byte("EUMETRIA_TAG=2\n"), 0o644)
	if _, err := u.Start(context.Background(), "1", ""); !errors.Is(err, ErrDowngrade) {
		t.Errorf("down a major: %v", err)
	}
	if _, err := u.Start(context.Background(), "2.0.1", ""); !errors.Is(err, ErrTag) {
		t.Errorf("pinned tag: %v", err)
	}
	w.mu.Lock() // compose blocks: the first update stays running
	if _, err := u.Start(context.Background(), "2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Start(context.Background(), "2", ""); !errors.Is(err, ErrBusy) {
		t.Errorf("second at once: %v", err)
	}
	w.mu.Unlock()
	wait(t, u)
}

func TestAPI(t *testing.T) {
	w := newWorld(t)
	w.release("1", "1.1.0")
	u := w.updater()
	tok, err := Token(w.dir)
	if err != nil || len(tok) != 64 {
		t.Fatal(tok, err)
	}
	if again, _ := Token(w.dir); again != tok {
		t.Error("a new token on restart")
	}
	srv := httptest.NewServer(u.Handler(tok))
	defer srv.Close()
	call := func(method, path, auth, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := call("GET", "/healthz", "", ""); code != 200 {
		t.Errorf("healthz %d", code)
	}
	for _, auth := range []string{"", "wrong", tok[:63]} {
		if code, _ := call("GET", "/status", auth, ""); code != 401 {
			t.Errorf("status with %q: %d", auth, code)
		}
	}
	code, st := call("GET", "/status", tok, "")
	if code != 200 || st["host"] != "vps-1" || st["tag"] != "1" || len(st["services"].([]any)) != 3 {
		t.Fatalf("status %d %v", code, st)
	}
	code, chk := call("POST", "/check", tok, "")
	imgs, _ := chk["images"].([]any)
	if code != 200 || len(imgs) != 3 || imgs[1].(map[string]any)["newer"] != true {
		t.Errorf("check %d %v", code, chk)
	}
	if code, _ := call("POST", "/update", tok, `{"tag":"1.1.0"}`); code != 400 {
		t.Errorf("pinned tag: %d", code)
	}
	if code, j := call("POST", "/update", tok, `{"tag":"1","backup":"b.tar.gz.enc"}`); code != 202 || j["state"] != "running" {
		t.Errorf("update %d %v", code, j)
	}
	wait(t, u)
}

func TestOlderSchemaIsRefused(t *testing.T) {
	w := newWorld(t)
	os.WriteFile(filepath.Join(w.dir, ".env"), []byte("EUMETRIA_TAG=latest\n"), 0o644)
	w.images["sha-app-1.0"].Labels["eumetria.schema"] = "35" // the edge build runs
	w.release("1", "1.1.0")
	w.images["sha-eumetria-1.1.0"].Labels["eumetria.schema"] = "34"
	u := w.updater()
	u.Start(context.Background(), "1", "")
	j := wait(t, u)
	if j.State != "failed" || !strings.Contains(j.Error, "older database schema") || u.Tag() != "latest" {
		t.Fatalf("job %+v, tag %s", j, u.Tag())
	}
	for _, c := range w.calls {
		if c[0] == "up" {
			t.Fatalf("restarted: %q", w.calls)
		}
	}
}
