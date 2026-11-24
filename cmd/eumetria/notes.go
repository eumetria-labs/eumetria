package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/notes"
	"eumetria/internal/picture"
	"eumetria/internal/sessions"
)

// Workout notes (PLAN M6), the first thing people write through the app.
// A note belongs to a training (PLAN 14), and is reached through a Fitbit
// workout of it: {key} is the workout's id. Everyone sees the notes of the
// workouts in their own stats, and those of trainings without a workout;
// admins also see, and can delete, the notes whose workouts have all gone
// from the synced data.

const (
	maxNote = 20 << 10 // bytes of Markdown
	// What that can take as JSON, where a character can become a six-byte
	// \u escape.
	maxNoteBody = 128 << 10
	maxImage    = 3 << 20 // bytes of an uploaded image
	// An image comes as base64 in JSON, like every other change, so the
	// cross-site check applies as it is. deploy/nginx*.conf let this through
	// for /api/notes/.
	maxImageBody = 5 << 20
	// How long an image no note uses may wait for the note being written.
	imageGrace = 24 * time.Hour
	// Limits on uploads (pentest F21): images no note embeds yet per user,
	// uploads per user and hour, and the size of the whole folder.
	maxPendingImages = 20
	maxImagesPerHour = 30
)

// maxImageDir is how big note-images/ may get; a variable for the tests.
var maxImageDir int64 = 2 << 30

// notesList answers GET /api/notes, optionally only the workouts from and
// to two days (YYYY-MM-DD, both included).
func (a *app) notesList(w http.ResponseWriter, req *http.Request, p *principal) {
	q := req.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	for _, day := range []string{from, to} {
		if _, err := time.Parse(time.DateOnly, day); day != "" && err != nil {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
	}
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	list, err := a.notes.List(req.Context())
	if err != nil {
		log.Printf("notes: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	out := []notes.Note{}
	for _, n := range list {
		date, found := noteDay(b, n)
		if !found || (from != "" && date < from) || (to != "" && date > to) {
			continue
		}
		n.Date = date
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, out)
}

// noteDay is the day of a note's first workout in b, and whether it has
// one there: a note of a training without workouts has its training's.
func noteDay(b *build, n notes.Note) (string, bool) {
	if len(n.Workouts) == 0 {
		return n.Date, true
	}
	for _, r := range n.Workouts {
		if date, found := b.workouts[r.ID]; found {
			return date, true
		}
	}
	return "", false
}

// The keys of a note in its routes: the id of one of its training's Fitbit
// workouts, trainingKey + the training's id (a training without workouts
// has no other), or newKey: the training of a form not saved yet, for
// images and photos only (PLAN 14b).
const (
	trainingKey = "training-"
	newKey      = "new"
)

// errNoTarget: the key names nothing the principal can reach.
var errNoTarget = errors.New("no such note target")

// noteTarget resolves a note key to its training (0: none yet) and day.
// With b, the training must be in the principal's stats: a workout there,
// or a training with one there or none at all; a nil b skips that (admins
// deleting notes whose workouts are gone). create makes the training of a
// workout that has none (a note being saved); newKey is then refused.
func (a *app) noteTarget(ctx context.Context, b *build, key string, create bool, userID int64) (int64, string, error) {
	if key == newKey {
		if create {
			return 0, "", errNoTarget
		}
		return 0, "", nil
	}
	if rest, ok := strings.CutPrefix(key, trainingKey); ok {
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || id <= 0 {
			return 0, "", errNoTarget
		}
		x, err := a.training.Get(ctx, id)
		if errors.Is(err, sessions.ErrNotFound) {
			return 0, "", errNoTarget
		}
		if err != nil {
			return 0, "", err
		}
		if b == nil || len(x.Workouts) == 0 {
			return x.ID, x.Date, nil
		}
		for _, r := range x.Workouts {
			if _, found := b.workouts[r.ID]; found && r.Source == sessions.Fitbit {
				return x.ID, x.Date, nil
			}
		}
		return 0, "", errNoTarget
	}
	date, found := "", b == nil
	if b != nil {
		date, found = b.workouts[key]
	}
	if !found {
		return 0, "", errNoTarget
	}
	ref := sessions.Ref{Source: sessions.Fitbit, ID: key}
	if create {
		id, err := a.training.Resolve(ctx, ref, date, nil, userID)
		return id, date, err
	}
	id, _, err := a.training.Lookup(ctx, ref)
	return id, date, err
}

// noteTargetFailed answers a failed noteTarget, and reports whether it did.
func (a *app) noteTargetFailed(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errNoTarget):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		a.failed(w, "notes", err)
	}
	return true
}

// notePut adds a workout's note or changes the one it has.
func (a *app) notePut(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Markdown string `json:"markdown"`
	}
	if !readJSONUpTo(w, req, &in, maxNoteBody) {
		return
	}
	key := req.PathValue("key")
	text := strings.TrimSpace(in.Markdown)
	if text == "" {
		writeError(w, http.StatusBadRequest, "empty")
		return
	}
	if len(text) > maxNote {
		writeError(w, http.StatusRequestEntityTooLarge, "too_long")
		return
	}
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	session, date, err := a.noteTarget(req.Context(), b, key, true, p.user.ID)
	if a.noteTargetFailed(w, err) {
		return
	}
	n, added, err := a.notes.Put(req.Context(), session, text, p.user.ID)
	if err != nil {
		log.Printf("note %s: %v", key, err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	n.Date = date
	action, code := "note_changed", http.StatusOK
	if added {
		action, code = "note_added", http.StatusCreated
	}
	a.audit_(req.Context(), p.user, "", action, date+" "+key, clientIP(req))
	writeJSON(w, code, n)
}

// noteDelete removes a note: of a workout in the user's stats, or for an
// admin also one whose workout is gone.
func (a *app) noteDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	key := req.PathValue("key")
	// An admin reaches the notes whose workouts are gone, too.
	session, date, err := a.noteTarget(req.Context(), b, key, false, p.user.ID)
	if errors.Is(err, errNoTarget) && p.user.Can(auth.ScopeAdmin) {
		session, date, err = a.noteTarget(req.Context(), nil, key, false, p.user.ID)
	}
	if err == nil && session == 0 {
		err = notes.ErrNotFound
	}
	if err == nil {
		err = a.notes.Delete(req.Context(), session)
	}
	if errors.Is(err, errNoTarget) {
		err = notes.ErrNotFound
	}
	if errors.Is(err, notes.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		log.Printf("note %s: %v", key, err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.audit_(req.Context(), p.user, "", "note_deleted", strings.TrimSpace(date+" "+key), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

// noteImagePost takes an image for the workout's note and answers with the
// address to embed. The note itself is saved later, or never (imageLoop).
func (a *app) noteImagePost(w http.ResponseWriter, req *http.Request, p *principal) {
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	key := req.PathValue("key")
	_, date, err := a.noteTarget(req.Context(), b, key, false, p.user.ID)
	if a.noteTargetFailed(w, err) {
		return
	}
	if !a.imageRoom(w, req.Context(), p) {
		return
	}
	held, ok := imageSlot(w, req)
	if !ok {
		return
	}
	defer held.Release()
	var in struct {
		Image []byte `json:"image"` // base64
	}
	if !readJSONUpTo(w, req, &in, maxImageBody) {
		return
	}
	if len(in.Image) > maxImage {
		writeError(w, http.StatusRequestEntityTooLarge, "too_long")
		return
	}
	name, err := notes.SaveImage(held, a.noteImages, in.Image)
	in.Image = nil
	held.Release()
	if errors.Is(err, picture.ErrBad) {
		writeError(w, http.StatusBadRequest, "bad_image")
		return
	}
	if err != nil {
		log.Printf("note %s: image: %v", key, err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if err := a.notes.RecordUpload(req.Context(), name, p.user.ID); err != nil {
		log.Printf("note %s: image: %v", key, err)
	}
	a.uploads.Fail("upload:" + strconv.FormatInt(p.user.ID, 10))
	a.audit_(req.Context(), p.user, "", "note_image_added", date+" "+key, clientIP(req))
	writeJSON(w, http.StatusCreated, map[string]string{"src": "note-images/" + name})
}

// noteImage serves an image of a note.
func (a *app) noteImage(w http.ResponseWriter, req *http.Request, p *principal) {
	if _, ok := a.build(w, req, p); !ok {
		return
	}
	name := req.PathValue("name")
	if !notes.ValidImageName(name) {
		http.NotFound(w, req)
		return
	}
	data, err := os.ReadFile(filepath.Join(a.noteImages, name))
	if err != nil {
		http.NotFound(w, req)
		return
	}
	personal(w, req, "image/jpeg", data)
}

// imageSlot takes the image slot for an upload before its body is read
// (pentest re-test N3): uploads waiting for the slot would each hold their
// body in memory, and enough of them kill the container. A body larger
// than maxImageBody by its Content-Length gets 413, one without a length
// 411, and a busy slot 503, all without reading a byte.
func imageSlot(w http.ResponseWriter, req *http.Request) (picture.Held, bool) {
	switch {
	case req.ContentLength < 0:
		writeError(w, http.StatusLengthRequired, "length_required")
		return picture.Held{}, false
	case req.ContentLength > maxImageBody:
		writeError(w, http.StatusRequestEntityTooLarge, "too_long")
		return picture.Held{}, false
	}
	held, err := picture.Acquire()
	if err != nil {
		busy(w)
		return picture.Held{}, false
	}
	return held, true
}

// imageRoom answers 429 or 507 itself when p may not upload another image
// now: too many uploads within the hour, too many no note embeds yet, or
// the folder is full.
func (a *app) imageRoom(w http.ResponseWriter, ctx context.Context, p *principal) bool {
	if a.uploads.Failures("upload:"+strconv.FormatInt(p.user.ID, 10)) >= maxImagesPerHour {
		writeError(w, http.StatusTooManyRequests, "too_many_images")
		return false
	}
	pending, err := a.notes.PendingUploads(ctx, p.user.ID)
	if err != nil {
		a.failed(w, "note image", err)
		return false
	}
	if pending >= maxPendingImages {
		writeError(w, http.StatusTooManyRequests, "too_many_pending")
		return false
	}
	size, err := notes.DirSize(a.noteImages)
	if err != nil {
		a.failed(w, "note image", err)
		return false
	}
	if size >= maxImageDir {
		log.Printf("note images: the folder is full (%d bytes)", size)
		writeError(w, http.StatusInsufficientStorage, "storage_full")
		return false
	}
	return true
}

// imageLoop removes the images no note uses, at start and every hour.
func imageLoop(ctx context.Context, s *notes.Store, dir string) {
	for {
		n, err := s.PruneImages(ctx, dir, time.Now().Add(-imageGrace))
		if err != nil {
			log.Printf("note images: %v", err)
		} else if n > 0 {
			log.Printf("note images: removed %d no note uses", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// adminNotes lists the notes whose workout is no longer in the synced data.
func (a *app) adminNotes(w http.ResponseWriter, req *http.Request, p *principal) {
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	list, err := a.notes.List(req.Context())
	if err != nil {
		a.failed(w, "notes", err)
		return
	}
	out := []notes.Note{}
	for _, n := range list {
		if _, found := noteDay(b, n); !found {
			out = append(out, n)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
