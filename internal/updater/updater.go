// Package updater is the eumetria-updater container (PLAN phase 15b): the
// one part of an installation with the Docker socket, kept as narrow as it
// can be. Eumetria asks it, over an internal network and with a token, to
// check for newer images or to update to a tag; nothing else.
//
// What it will touch: the containers of its own compose project whose
// image is one of Eumetria's (Prefix, Prefix-claude-runner,
// Prefix-updater), and the tag in the project's .env (EUMETRIA_TAG, which
// compose.yaml uses for all three). The tag is "latest" (edge) or a major
// version ("1"); it never goes down a major. Other services of the project,
// other images and other tags can't be reached through it.
//
// An update pulls the images, recreates the containers with docker compose
// (so compose.yaml stays the truth), waits for Eumetria's health check and
// goes back to the old images if it doesn't pass. The updater replaces
// itself last, through a short-lived helper container.
package updater

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TagKey is the variable compose.yaml takes the tag from.
const TagKey = "EUMETRIA_TAG"

// RollbackTag keeps the images that ran before an update. A pull moves the
// channel's tag to the new image, and an image without a tag may be gone
// by its ID (Docker's containerd image store): this tag keeps it.
const RollbackTag = "rollback"

// DefaultTag is the tag when .env doesn't set one, as compose.yaml's
// ${EUMETRIA_TAG:-1}.
const DefaultTag = "1"

var validTag = regexp.MustCompile(`^(latest|[1-9][0-9]{0,2})$`)

// Errors a caller can tell apart.
var (
	ErrTag       = errors.New("the tag must be latest or a major version number")
	ErrDowngrade = errors.New("going down a major version isn't possible: the database may be newer")
	ErrBusy      = errors.New("an update is running")
	ErrSchema    = errors.New("the new image has an older database schema than the one running")
)

// Compose runs docker compose for the project with args and answers its
// output.
type Compose func(ctx context.Context, args ...string) (string, error)

// Job is one update, as it goes and as it ended.
type Job struct {
	ID       string            `json:"id"`
	Tag      string            `json:"tag"`
	From     string            `json:"from"` // the tag before
	Backup   string            `json:"backup,omitempty"`
	Started  time.Time         `json:"started"`
	Finished *time.Time        `json:"finished,omitempty"`
	State    string            `json:"state"` // running, done, unchanged, rolled_back, failed
	Step     string            `json:"step"`  // pull, restart, health, rollback, handover
	Error    string            `json:"error,omitempty"`
	Before   map[string]string `json:"before,omitempty"` // service → version label
	After    map[string]string `json:"after,omitempty"`
	Log      string            `json:"log,omitempty"` // Eumetria's last lines when it failed
	Handover bool              `json:"handover,omitempty"`
}

// Updater does the work. Its fields are set once at the start.
type Updater struct {
	Docker  Docker
	Compose Compose
	Project string   // the compose project
	Dir     string   // its directory, mounted at the same path as on the host
	Files   []string // its compose files
	Prefix  string   // ghcr.io/eumetria-labs/eumetria
	Self    string   // the updater's own container ID
	State   string   // folder for the token and the history
	Now     func() time.Time
	// HealthWait is how long Eumetria may take to become healthy; Poll how
	// often that is looked at.
	HealthWait, Poll time.Duration

	mu  sync.Mutex
	job *Job
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

/* ---------- the project's services ---------- */

// service is one of Eumetria's containers in the project.
type service struct {
	Name      string // compose service
	Container Container
	Repo      string // image without the tag
	Kind      string // eumetria, claude-runner or updater
}

// repoKind answers which of Eumetria's images repo is, or "".
func (u *Updater) repoKind(repo string) string {
	switch repo {
	case u.Prefix:
		return "eumetria"
	case u.Prefix + "-claude-runner":
		return "claude-runner"
	case u.Prefix + "-updater":
		return "updater"
	}
	return ""
}

// splitRef splits an image ref into repository and tag.
func splitRef(ref string) (repo, tag string) {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// services are the project's containers with one of Eumetria's images.
func (u *Updater) services(ctx context.Context) ([]service, error) {
	list, err := u.Docker.Project(ctx, u.Project)
	if err != nil {
		return nil, err
	}
	var out []service
	for _, c := range list {
		// Not the handover helper, nor one-off containers (docker compose
		// run … -create-user): they carry the service's labels too.
		if c.Labels["eumetria.updater"] == "handover" || c.Labels["com.docker.compose.oneoff"] == "True" {
			continue
		}
		repo, _ := splitRef(c.Ref)
		if kind := u.repoKind(repo); kind != "" {
			out = append(out, service{Name: c.Labels["com.docker.compose.service"], Container: c, Repo: repo, Kind: kind})
		}
	}
	slices.SortFunc(out, func(a, b service) int { return strings.Compare(a.Name, b.Name) })
	if !slices.ContainsFunc(out, func(s service) bool { return s.Kind == "eumetria" }) {
		return nil, fmt.Errorf("no container of %s in the compose project %q", u.Prefix, u.Project)
	}
	return out, nil
}

func names(ss []service, skip string) []string {
	var out []string
	for _, s := range ss {
		if s.Kind != skip {
			out = append(out, s.Name)
		}
	}
	return out
}

/* ---------- .env ---------- */

func (u *Updater) envFile() string { return filepath.Join(u.Dir, ".env") }

// Tag is the tag in .env, or the default.
func (u *Updater) Tag() string {
	b, err := os.ReadFile(u.envFile())
	if err != nil {
		return DefaultTag
	}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.TrimSpace(k) == TagKey {
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if validTag.MatchString(v) {
				return v
			}
		}
	}
	return DefaultTag
}

// setTag writes tag into .env, keeping its other lines.
func (u *Updater) setTag(tag string) error {
	b, err := os.ReadFile(u.envFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var lines []string
	done := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if k, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok && strings.TrimSpace(k) == TagKey {
			if !done {
				lines = append(lines, TagKey+"="+tag)
				done = true
			}
			continue
		}
		if l != "" || len(lines) > 0 {
			lines = append(lines, l)
		}
	}
	if !done {
		lines = append(lines, "# Written by eumetria-updater: the channel (latest = edge, a number = stable on that major).", TagKey+"="+tag)
	}
	tmp := u.envFile() + ".new"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, u.envFile())
}

// CheckTag says whether going from the current tag to tag is allowed.
func CheckTag(from, tag string) error {
	if !validTag.MatchString(tag) {
		return ErrTag
	}
	a, errA := strconv.Atoi(from)
	b, errB := strconv.Atoi(tag)
	if errA == nil && errB == nil && b < a {
		return ErrDowngrade
	}
	return nil
}

/* ---------- checking ---------- */

// Newer is one of Eumetria's images and whether the registry has a newer
// one under the same tag.
type Newer struct {
	Service string `json:"service"`
	Ref     string `json:"ref"`
	Version string `json:"version,omitempty"`
	Local   string `json:"local,omitempty"`
	Remote  string `json:"remote,omitempty"`
	Newer   bool   `json:"newer"`
	Error   string `json:"error,omitempty"`
}

// Check compares each image with the registry's, for the current tag.
func (u *Updater) Check(ctx context.Context) ([]Newer, error) {
	ss, err := u.services(ctx)
	if err != nil {
		return nil, err
	}
	tag := u.Tag()
	var out []Newer
	for _, s := range ss {
		ref := s.Repo + ":" + tag
		n := Newer{Service: s.Name, Ref: ref}
		if img, err := u.Docker.Image(ctx, s.Container.Image); err == nil {
			n.Version = img.Labels["org.opencontainers.image.version"]
			for _, d := range img.RepoDigests {
				if r, dg, ok := strings.Cut(d, "@"); ok && r == s.Repo {
					n.Local = dg
				}
			}
		}
		remote, err := u.Docker.RemoteDigest(ctx, ref)
		if err != nil {
			n.Error = err.Error()
		} else {
			n.Remote = remote
			n.Newer = n.Local != remote
		}
		out = append(out, n)
	}
	return out, nil
}

/* ---------- updating ---------- */

// Current is the running job, or nil.
func (u *Updater) Current() *Job {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.job == nil {
		return nil
	}
	j := *u.job
	return &j
}

// Start begins an update to tag in the background and answers its job.
// backup names the backup Eumetria made before asking.
func (u *Updater) Start(ctx context.Context, tag, backup string) (*Job, error) {
	from := u.Tag()
	if err := CheckTag(from, tag); err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.job != nil {
		return nil, ErrBusy
	}
	now := u.now().UTC()
	u.job = &Job{ID: now.Format("20060102T150405Z"), Tag: tag, From: from, Backup: backup, Started: now, State: "running", Step: "pull"}
	j := *u.job
	go func() {
		defer func() {
			if r := recover(); r != nil {
				u.finish("failed", fmt.Errorf("panic: %v", r))
			}
		}()
		u.run(context.WithoutCancel(ctx))
	}()
	return &j, nil
}

func (u *Updater) step(s string) {
	u.mu.Lock()
	u.job.Step = s
	u.mu.Unlock()
	log.Printf("update: %s", s)
}

func (u *Updater) set(f func(j *Job)) {
	u.mu.Lock()
	f(u.job)
	u.mu.Unlock()
}

// finish ends the job and records it in the history.
func (u *Updater) finish(state string, err error) {
	u.mu.Lock()
	j := u.job
	if j == nil {
		u.mu.Unlock()
		return
	}
	t := u.now().UTC()
	j.State, j.Finished = state, &t
	if err != nil {
		j.Error = err.Error()
	}
	done := *j
	u.job = nil
	u.mu.Unlock()
	log.Printf("update to %s: %s %s", done.Tag, done.State, done.Error)
	if err := u.record(done); err != nil {
		log.Printf("history: %v", err)
	}
}

func (u *Updater) versions(ctx context.Context, ss []service, ref func(service) string) map[string]string {
	out := map[string]string{}
	for _, s := range ss {
		img, err := u.Docker.Image(ctx, ref(s))
		if err != nil {
			log.Printf("update: the version of %s: %v", s.Name, err)
			continue
		}
		out[s.Name] = img.Labels["org.opencontainers.image.version"]
	}
	return out
}

func (u *Updater) run(ctx context.Context) {
	j := u.Current()
	ss, err := u.services(ctx)
	if err != nil {
		u.finish("failed", err)
		return
	}
	u.set(func(x *Job) { x.Before = u.versions(ctx, ss, func(s service) string { return s.Container.Image }) })

	// The new tag, and compose.yaml must take it for all of Eumetria's
	// images: else nothing would change and the check after it would lie.
	if j.Tag != j.From {
		if err := u.setTag(j.Tag); err != nil {
			u.finish("failed", err)
			return
		}
	}
	restore := func() {
		if j.Tag != j.From {
			if err := u.setTag(j.From); err != nil {
				log.Printf("update: putting the tag back: %v", err)
			}
		}
	}
	out, err := u.Compose(ctx, "config", "--images")
	if err != nil {
		restore()
		u.finish("failed", fmt.Errorf("docker compose config: %v: %s", err, tail(out)))
		return
	}
	images := strings.Fields(out)
	for _, s := range ss {
		if !slices.Contains(images, s.Repo+":"+j.Tag) {
			restore()
			u.finish("failed", fmt.Errorf("compose.yaml doesn't use ${%s} for %s (it names %s)", TagKey, s.Repo, strings.Join(images, ", ")))
			return
		}
	}

	// Keep what runs now, for going back.
	for _, s := range ss {
		if err := u.Docker.Tag(ctx, s.Container.Image, s.Repo, RollbackTag); err != nil {
			restore()
			u.finish("failed", fmt.Errorf("keeping %s for going back: %v", s.Name, err))
			return
		}
	}

	u.step("pull")
	if out, err := u.Compose(ctx, append([]string{"pull", "--quiet"}, names(ss, "")...)...); err != nil {
		restore()
		u.finish("failed", fmt.Errorf("docker compose pull: %v: %s", err, tail(out)))
		return
	}
	changed, updaterChanged := false, false
	for _, s := range ss {
		img, err := u.Docker.Image(ctx, s.Repo+":"+j.Tag)
		if err != nil {
			restore()
			u.finish("failed", err)
			return
		}
		if img.ID != s.Container.Image {
			changed = true
			updaterChanged = updaterChanged || s.Kind == "updater"
		}
	}
	if !changed {
		u.set(func(x *Job) { x.After = x.Before })
		u.finish("unchanged", nil)
		return
	}
	// An older schema can't open the database the running one migrated
	// (edge → stable before stable caught up): stop before anything
	// restarts. Images without the label (built by hand) aren't compared.
	main := u.find(ss, "eumetria")
	if err := u.schemaOK(ctx, main.Container.Image, main.Repo+":"+j.Tag); err != nil {
		restore()
		u.finish("failed", err)
		return
	}

	u.step("restart")
	others := names(ss, "updater")
	if out, err := u.Compose(ctx, append([]string{"up", "--detach", "--no-deps"}, others...)...); err != nil {
		u.rollback(ctx, ss, j, fmt.Errorf("docker compose up: %v: %s", err, tail(out)))
		return
	}
	u.step("health")
	if err := u.healthy(ctx, ss); err != nil {
		u.rollback(ctx, ss, j, err)
		return
	}
	u.set(func(x *Job) { x.After = u.versions(ctx, ss, func(s service) string { return s.Repo + ":" + j.Tag }) })

	if updaterChanged {
		u.step("handover")
		u.set(func(x *Job) { x.Handover = true })
		u.finish("done", nil)
		self := u.find(ss, "updater")
		if err := u.Docker.Handover(ctx, self.Repo+":"+j.Tag, u.Self, []string{"-handover", "-project", u.Project, "-dir", u.Dir, "-service", self.Name, "-files", strings.Join(u.Files, ",")}); err != nil {
			log.Printf("update: the updater couldn't replace itself: %v (it stays on the old image until restarted)", err)
		}
		return
	}
	u.finish("done", nil)
}

// schemaOK compares the eumetria.schema labels of the running image and
// the new one.
func (u *Updater) schemaOK(ctx context.Context, running, next string) error {
	a, errA := u.Docker.Image(ctx, running)
	b, errB := u.Docker.Image(ctx, next)
	if errA != nil || errB != nil {
		return nil
	}
	have, err1 := strconv.Atoi(a.Labels["eumetria.schema"])
	want, err2 := strconv.Atoi(b.Labels["eumetria.schema"])
	if err1 != nil || err2 != nil || have == 0 || want == 0 || want >= have {
		return nil
	}
	return fmt.Errorf("%w (%d, running %d): the switch waits for a release that has caught up", ErrSchema, want, have)
}

func (u *Updater) find(ss []service, kind string) service {
	for _, s := range ss {
		if s.Kind == kind {
			return s
		}
	}
	return service{}
}

// healthy waits for Eumetria to pass its health check and the others to
// run.
func (u *Updater) healthy(ctx context.Context, ss []service) error {
	wait, poll := u.HealthWait, u.Poll
	if wait == 0 {
		wait = 3 * time.Minute
	}
	if poll == 0 {
		poll = 2 * time.Second
	}
	deadline := u.now().Add(wait)
	main := u.find(ss, "eumetria")
	for {
		c, err := u.Docker.Container(ctx, main.Container.Name)
		switch {
		case err != nil:
			// Recreated: the old ID is gone, the name stays.
		case c.State == "running" && (c.Health == "healthy" || c.Health == ""):
			for _, s := range ss {
				if s.Kind == "eumetria" || s.Kind == "updater" {
					continue
				}
				if o, err := u.Docker.Container(ctx, s.Container.Name); err != nil || o.State != "running" {
					return fmt.Errorf("%s doesn't run after the update", s.Name)
				}
			}
			return nil
		case c.State == "exited" || c.State == "dead" || c.Health == "unhealthy":
			return u.failedWith(ctx, c, fmt.Sprintf("Eumetria is %s after the update", strings.TrimSpace(c.Health+" "+c.State)))
		}
		if u.now().After(deadline) {
			return u.failedWith(ctx, c, fmt.Sprintf("Eumetria wasn't healthy within %s", wait))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// failedWith keeps the failing container's last log lines in the job.
func (u *Updater) failedWith(ctx context.Context, c Container, why string) error {
	if c.ID != "" {
		if l, err := u.Docker.Logs(ctx, c.ID, 30); err == nil {
			u.set(func(x *Job) { x.Log = tail(l) })
		}
	}
	return errors.New(why)
}

// rollback goes back to the images the containers ran before: the ones
// kept under RollbackTag get the old tag again (a pull under the same tag
// moved it), .env the old tag, and the containers are recreated.
func (u *Updater) rollback(ctx context.Context, ss []service, j *Job, cause error) {
	u.step("rollback")
	log.Printf("update: %v; going back", cause)
	for _, s := range ss {
		if err := u.Docker.Tag(ctx, s.Repo+":"+RollbackTag, s.Repo, j.From); err != nil {
			u.finish("failed", fmt.Errorf("%v; going back failed too: tagging %s: %v", cause, s.Name, err))
			return
		}
	}
	if j.Tag != j.From {
		if err := u.setTag(j.From); err != nil {
			u.finish("failed", fmt.Errorf("%v; going back failed too: %v", cause, err))
			return
		}
	}
	if out, err := u.Compose(ctx, append([]string{"up", "--detach", "--no-deps"}, names(ss, "updater")...)...); err != nil {
		u.finish("failed", fmt.Errorf("%v; going back failed too: docker compose up: %v: %s", cause, err, tail(out)))
		return
	}
	if err := u.healthy(ctx, ss); err != nil {
		u.finish("failed", fmt.Errorf("%v; after going back: %v (restore the backup made before the update)", cause, err))
		return
	}
	u.finish("rolled_back", cause)
}

// tail is the last lines of a command's output or a log, for a message.
func tail(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	s = strings.Join(lines, "\n")
	if len(s) > 4000 {
		s = s[len(s)-4000:]
	}
	return s
}

/* ---------- the history ---------- */

const keepJobs = 20

func (u *Updater) historyFile() string { return filepath.Join(u.State, "history.json") }

// History is the finished updates, newest first.
func (u *Updater) History() []Job {
	b, err := os.ReadFile(u.historyFile())
	if err != nil {
		return nil
	}
	var jobs []Job
	json.Unmarshal(b, &jobs)
	return jobs
}

func (u *Updater) record(j Job) error {
	jobs := append([]Job{j}, u.History()...)
	if len(jobs) > keepJobs {
		jobs = jobs[:keepJobs]
	}
	b, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	tmp := u.historyFile() + ".new"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, u.historyFile())
}
