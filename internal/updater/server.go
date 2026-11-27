package updater

// The updater's API, for Eumetria only: no published port, an internal
// network, and a token the updater makes on its first start and keeps in
// the volume Eumetria mounts read-only.
//
//	GET  /healthz  ok (no token: the container's own health check)
//	GET  /status   the tag, the services and their versions, the host, the running job, the history
//	POST /check    compares the images with the registry's
//	POST /update   {tag, backup}: starts an update; 202 with the job

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// TokenFile is the token's file name in the state folder.
const TokenFile = "token"

// Token reads the token in dir, making one on the first start.
func Token(dir string) (string, error) {
	file := filepath.Join(dir, TokenFile)
	if b, err := os.ReadFile(file); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	// Readable for Eumetria, which runs as another user and mounts the
	// volume read-only; no other container mounts it.
	if err := os.WriteFile(file, []byte(tok+"\n"), 0o644); err != nil {
		return "", err
	}
	return tok, nil
}

// Status is what GET /status answers.
type Status struct {
	Tag      string    `json:"tag"`
	Host     string    `json:"host,omitempty"`
	Project  string    `json:"project"`
	Services []Running `json:"services"`
	Job      *Job      `json:"job,omitempty"`
	History  []Job     `json:"history"`
}

// Running is one of Eumetria's containers as it runs now.
type Running struct {
	Service  string `json:"service"`
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision,omitempty"`
	Schema   string `json:"schema,omitempty"`
	State    string `json:"state"`
	Health   string `json:"health,omitempty"`
}

// Status describes the installation.
func (u *Updater) Status(ctx context.Context) (Status, error) {
	st := Status{Tag: u.Tag(), Project: u.Project, Job: u.Current(), History: u.History()}
	if st.History == nil {
		st.History = []Job{}
	}
	st.Host, _ = u.Docker.HostName(ctx)
	ss, err := u.services(ctx)
	if err != nil {
		return st, err
	}
	for _, s := range ss {
		r := Running{Service: s.Name, Kind: s.Kind, Ref: s.Container.Ref, State: s.Container.State, Health: s.Container.Health}
		if img, err := u.Docker.Image(ctx, s.Container.Image); err == nil {
			r.Version = img.Labels["org.opencontainers.image.version"]
			r.Revision = img.Labels["org.opencontainers.image.revision"]
			r.Schema = img.Labels["eumetria.schema"]
		}
		st.Services = append(st.Services, r)
	}
	return st, nil
}

// Handler is the API, with token as the only key.
func (u *Updater) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				reply(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /status", guard(func(w http.ResponseWriter, r *http.Request) {
		st, err := u.Status(r.Context())
		if err != nil {
			reply(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "status": st})
			return
		}
		reply(w, http.StatusOK, st)
	}))
	mux.HandleFunc("POST /check", guard(func(w http.ResponseWriter, r *http.Request) {
		list, err := u.Check(r.Context())
		if err != nil {
			reply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		reply(w, http.StatusOK, map[string]any{"tag": u.Tag(), "images": list})
	}))
	mux.HandleFunc("POST /update", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Tag, Backup string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
			reply(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
			return
		}
		j, err := u.Start(r.Context(), in.Tag, in.Backup)
		switch {
		case errors.Is(err, ErrTag):
			reply(w, http.StatusBadRequest, map[string]string{"error": "bad_tag"})
		case errors.Is(err, ErrDowngrade):
			reply(w, http.StatusConflict, map[string]string{"error": "downgrade"})
		case errors.Is(err, ErrBusy):
			reply(w, http.StatusConflict, map[string]string{"error": "busy"})
		case err != nil:
			reply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			reply(w, http.StatusAccepted, j)
		}
	}))
	return mux
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
