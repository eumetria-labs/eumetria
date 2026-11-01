package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/notes"
	"eumetria/internal/picture"
)

// Workout notes (PLAN M6), the first thing people write through the app.
// A note belongs to a Fitbit workout. Everyone sees the notes of the
// workouts in their own stats; admins also see, and can delete, the notes
// whose workout has gone from the synced data.

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
	imageGrace = 7 * 24 * time.Hour
)

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
		date, found := b.workouts[n.WorkoutID]
		if !found || (from != "" && date < from) || (to != "" && date > to) {
			continue
		}
		n.Date = date
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, out)
}

// notePut adds a workout's note or changes the one it has.
func (a *app) notePut(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Markdown string `json:"markdown"`
	}
	if !readJSONUpTo(w, req, &in, maxNoteBody) {
		return
	}
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	key := req.PathValue("key")
	date, found := b.workouts[key]
	if !found {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	text := strings.TrimSpace(in.Markdown)
	if text == "" {
		writeError(w, http.StatusBadRequest, "empty")
		return
	}
	if len(text) > maxNote {
		writeError(w, http.StatusRequestEntityTooLarge, "too_long")
		return
	}
	n, added, err := a.notes.Put(req.Context(), key, text, p.user.ID)
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
	date, found := b.workouts[key]
	if !found && !p.user.Can(auth.ScopeAdmin) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	err := a.notes.Delete(req.Context(), key)
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
	var in struct {
		Image []byte `json:"image"` // base64
	}
	if !readJSONUpTo(w, req, &in, maxImageBody) {
		return
	}
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	key := req.PathValue("key")
	date, found := b.workouts[key]
	if !found {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(in.Image) > maxImage {
		writeError(w, http.StatusRequestEntityTooLarge, "too_long")
		return
	}
	name, err := notes.SaveImage(a.noteImages, in.Image)
	if errors.Is(err, picture.ErrBad) {
		writeError(w, http.StatusBadRequest, "bad_image")
		return
	}
	if err != nil {
		log.Printf("note %s: image: %v", key, err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
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
	immutable(w, "image/jpeg")
	w.Write(data)
}

// imageLoop removes the images no note uses, at start and every night.
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
		case <-time.After(time.Until(nextAt(time.Now(), 3, 10))):
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
		if _, found := b.workouts[n.WorkoutID]; !found {
			out = append(out, n)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
