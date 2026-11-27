package main

// Installing an update (PLAN 15c): Eumetria makes a backup, then asks the
// updater container (updater.go) for the tag of the channel. The updater
// pulls, restarts and checks; Eumetria restarts with it, so what happened
// afterwards is in the updater's history, which Settings → Update shows.
//
//	POST /api/admin/update  {kind: "update" | "major"}: installs, with a backup first   admin, recent sign-in
//
// "update" installs what the channel has (the stable channel's major, or
// latest on edge, which also takes a switch of the channel); "major" moves
// the stable channel to the newer major version the last check announced,
// after the person did its steps. Automatic installs (the setting) run at
// the set time with the same steps; a major version never installs on its
// own.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/backup"
	"eumetria/internal/settings"
	"eumetria/internal/update"
)

var (
	errApplyBusy      = errors.New("an update is being started")
	errNoUpdateTarget = errors.New("nothing to install: check for updates first")
)

// applyState is an update on Eumetria's side: the backup, then asking the
// updater. Once the updater has it, its job says the rest.
type applyState struct {
	Running bool      `json:"running"`
	Step    string    `json:"step,omitempty"` // backup, asking, updating
	Tag     string    `json:"tag,omitempty"`
	Auto    bool      `json:"auto,omitempty"`
	Started time.Time `json:"started,omitzero"`
	Backup  string    `json:"backup,omitempty"`
	Job     string    `json:"job,omitempty"` // the updater's job
	Error   string    `json:"error,omitempty"`
}

func (u *updates) applying() applyState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.apply
}

func (u *updates) setApply(f func(s *applyState)) {
	u.mu.Lock()
	f(&u.apply)
	u.mu.Unlock()
}

// updateTag is the tag an update of kind installs: "latest" on edge; on
// stable the running release's major (an edge build switching to stable:
// the major of the newest release); for "major" the newer major of the
// last check.
func updateTag(kind, channel, running string, last *checkResult) (string, error) {
	b := update.ParseBuild(running)
	if kind == "major" {
		if last == nil || last.Major == nil {
			return "", errNoUpdateTarget
		}
		v, ok := update.ParseVersion(last.Major.Version)
		if !ok || b.Kind == "release" && v.Major <= b.Version.Major {
			return "", errNoUpdateTarget
		}
		return strconv.Itoa(v.Major), nil
	}
	if channel == update.Edge {
		return "latest", nil
	}
	if b.Kind == "release" {
		return strconv.Itoa(b.Version.Major), nil
	}
	if last != nil && last.Channel == update.Stable {
		if v, ok := update.ParseVersion(last.Target); ok {
			return strconv.Itoa(v.Major), nil
		}
	}
	return "", errNoUpdateTarget
}

// startApply begins an update in the background. u is who asked (nil for
// an automatic one).
func (a *app) startApply(ctx context.Context, kind string, u *auth.User, ip string, auto bool) error {
	c, err := newUpdaterClient()
	if err != nil {
		return err
	}
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		return err
	}
	tag, err := updateTag(kind, v.UpdateChannel, appVersion, a.lastCheck(ctx))
	if errors.Is(err, errNoUpdateTarget) && kind == "update" {
		a.checkUpdates(ctx)
		tag, err = updateTag(kind, v.UpdateChannel, appVersion, a.lastCheck(ctx))
	}
	if err != nil {
		return err
	}
	up := a.updates
	up.mu.Lock()
	if up.apply.Running {
		up.mu.Unlock()
		return errApplyBusy
	}
	up.apply = applyState{Running: true, Step: "backup", Tag: tag, Auto: auto, Started: time.Now().UTC()}
	up.mu.Unlock()
	actor := ""
	if auto {
		actor = "automatic"
	}
	a.audit_(ctx, u, actor, "update_started", "tag "+tag, ip)
	log.Printf("update: to the %s images, a backup first", tag)

	go func() {
		ctx := context.WithoutCancel(ctx)
		fail := func(err error) {
			log.Printf("update: %v", err)
			up.setApply(func(s *applyState) { s.Running, s.Error = false, err.Error() })
			a.audit_(ctx, u, actor, "update_failed", err.Error(), ip)
		}
		run, err := a.backupBeforeUpdate(ctx)
		if err != nil {
			fail(fmt.Errorf("the backup before the update failed, so nothing was installed: %w", err))
			return
		}
		up.setApply(func(s *applyState) { s.Step, s.Backup = "asking", run.File })
		job, err := c.Start(ctx, tag, run.File)
		if err != nil {
			fail(err)
			return
		}
		// Eumetria restarts in a moment; the updater's job says the rest.
		up.setApply(func(s *applyState) { s.Running, s.Step, s.Job = false, "updating", job.ID })
	}()
	return nil
}

// backupBeforeUpdate makes a backup now and waits for it: an update only
// starts with one, since a new version's migrations can't be undone.
func (a *app) backupBeforeUpdate(ctx context.Context) (backup.Run, error) {
	r := a.runner
	start := time.Now().Truncate(time.Second)
	// A sync or another job may run: wait for its end, a few minutes.
	for i := 0; !r.startBackup(ctx); i++ {
		if i > 180 {
			return backup.Run{}, errors.New("another job didn't end")
		}
		if err := sleepCtx(ctx, time.Second); err != nil {
			return backup.Run{}, err
		}
	}
	deadline := time.Now().Add(backupTimeout + time.Minute)
	for r.status().Running {
		if time.Now().After(deadline) {
			return backup.Run{}, errors.New("the backup didn't end")
		}
		if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
			return backup.Run{}, err
		}
	}
	runs, err := backup.Recent(ctx, r.settings.DB.DB, 1)
	if err != nil {
		return backup.Run{}, err
	}
	if len(runs) == 0 || runs[0].StartedAt.Before(start) {
		return backup.Run{}, errors.New("no backup was made")
	}
	if runs[0].Error != "" {
		return runs[0], errors.New(runs[0].Error)
	}
	return runs[0], nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

/* ---------- automatic installs ---------- */

// autoInstall installs what the channel has at the set time, once a day:
// a new version or a security rebuild of the running one. Never a new
// major version.
func (a *app) autoInstall(ctx context.Context, v settings.Values, now time.Time) {
	if !v.UpdateAuto || a.demo {
		return
	}
	var last time.Time
	if s, _ := a.updateState(ctx, "auto_at"); s != "" {
		last, _ = time.Parse(time.RFC3339, s)
	}
	if !backup.Due(now.In(v.Zone), v.UpdateTime, last.In(v.Zone)) {
		return
	}
	// Once a day, whatever comes of it.
	if err := a.setUpdateState(ctx, "auto_at", now.UTC().Format(time.RFC3339)); err != nil {
		log.Printf("update: %v", err)
		return
	}
	if _, err := newUpdaterClient(); err != nil {
		return // nothing to install with
	}
	// What's there now, also the registry's rebuilds.
	a.checkUpdates(ctx)
	check := a.lastCheck(ctx)
	if check == nil || check.Channel != v.UpdateChannel || !(check.Available || check.Rebuild) {
		return
	}
	if err := a.startApply(ctx, "update", nil, "", true); err != nil {
		log.Printf("update: the automatic install didn't start: %v", err)
	}
}

/* ---------- the API ---------- */

func (a *app) updateApply(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Kind string `json:"kind"`
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	if a.demo {
		writeError(w, http.StatusConflict, "demo")
		return
	}
	if in.Kind == "" {
		in.Kind = "update"
	}
	if in.Kind != "update" && in.Kind != "major" {
		writeError(w, http.StatusBadRequest, "bad_kind")
		return
	}
	err := a.startApply(context.WithoutCancel(req.Context()), in.Kind, p.user, clientIP(req), false)
	switch {
	case errors.Is(err, errNoUpdater):
		writeError(w, http.StatusConflict, "no_updater")
	case errors.Is(err, errApplyBusy):
		writeError(w, http.StatusConflict, "busy")
	case errors.Is(err, errNoUpdateTarget):
		writeError(w, http.StatusConflict, "no_target")
	case err != nil:
		a.failed(w, "update", err)
	default:
		a.updateGet(w, req, p)
	}
}
