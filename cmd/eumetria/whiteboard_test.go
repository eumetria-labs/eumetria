package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"eumetria/internal/ai"
	"eumetria/internal/settings"
)

// fakeVision answers Messages API requests (not streamed) about an image.
type fakeVision struct {
	mu     sync.Mutex
	images int
	answer string
}

func (f *fakeVision) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(req.Body).Decode(&body)
	f.mu.Lock()
	for _, c := range body.Messages[0].Content {
		if c["type"] == "image" {
			f.images++
		}
	}
	answer := f.answer
	f.mu.Unlock()
	b, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"m","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":%s}],"stop_reason":"end_turn","usage":{"input_tokens":1500,"output_tokens":60}}`, body.Model, b)
}

func TestWhiteboard(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	vision := &fakeVision{answer: "### WOD: AMRAP 12\n- 10 alternating DB snatch @ 15 kg\n- 200 m run"}
	api := httptest.NewServer(vision)
	t.Cleanup(api.Close)
	coachAPIURL = api.URL
	t.Cleanup(func() { coachAPIURL = "" })

	trainer := e.writer(t)
	res, out := e.do(t, trainer, "POST", "/api/notes/w1/images", `{"image":"`+uploadImage(t, "")+`"}`, sameSite)
	var up struct{ Src string }
	json.Unmarshal([]byte(out), &up)
	if res.StatusCode != 201 {
		t.Fatalf("upload = %d %s", res.StatusCode, out)
	}
	read := func(c *http.Client, key, src string) (int, map[string]any) {
		res, out := e.do(t, c, "POST", "/api/notes/"+key+"/transcribe", `{"src":"`+src+`"}`, sameSite)
		var m map[string]any
		json.Unmarshal([]byte(out), &m)
		return res.StatusCode, m
	}

	// No AI set up yet.
	if code, m := read(trainer, "w1", up.Src); code != 409 || m["error"] != "ai_off" {
		t.Errorf("without a key = %d %v", code, m)
	}
	e.runner.settings.Save(ctx, map[string]string{"anthropic_key": "sk-test", "whiteboard_own": "true", "whiteboard_model": "claude-haiku-5-5"}, nil)
	code, m := read(trainer, "w1", up.Src)
	if code != 200 || !strings.Contains(m["markdown"].(string), "alternating DB snatch @ 15 kg") || m["backend"] != "api" || m["model"] != "claude-haiku-5-5" {
		t.Fatalf("transcribe = %d %v", code, m)
	}
	if vision.images != 1 {
		t.Errorf("images sent: %d", vision.images)
	}

	// Only one's own uploads, or images the workout's note embeds.
	patrick := e.signIn(t, "patrick", adminPassword, false)
	if code, _ := read(patrick, "w1", up.Src); code != 404 {
		t.Errorf("someone else's upload = %d, want 404", code)
	}
	if code, _ := read(trainer, "nope", up.Src); code != 404 {
		t.Errorf("unknown workout = %d", code)
	}
	if code, _ := read(trainer, "w1", "../../etc/passwd"); code != 400 {
		t.Errorf("bad name = %d", code)
	}
	reader := e.signIn(t, "anna", viewerPassword, false)
	if code, _ := read(reader, "w1", up.Src); code != 403 {
		t.Errorf("a reader = %d, want 403", code)
	}
	if res, out := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"Board:\n\n![](`+up.Src+`)"}`, sameSite); res.StatusCode != 201 {
		t.Fatalf("save note = %d %s", res.StatusCode, out)
	}

	// The owner on Claude Code: the image goes to the runner.
	var job map[string]any
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		json.NewDecoder(req.Body).Decode(&job)
		fmt.Fprintln(w, `{"kind":"text","text":"### Strength\n- Back squat 5×5 @ 100 kg"}`)
		fmt.Fprintln(w, `{"kind":"done","model":"claude-opus-5-5"}`)
	}))
	t.Cleanup(rs.Close)
	whiteboardRunner = func(settings.Values) ai.Runner { return ai.Runner{URL: rs.URL, Key: "k"} }
	t.Cleanup(func() {
		whiteboardRunner = func(v settings.Values) ai.Runner { return ai.Runner{URL: v.RunnerURL, Key: v.RunnerKey} }
	})
	e.runner.settings.Save(ctx, map[string]string{"whiteboard_backend": "claude"}, nil)
	code, m = read(patrick, "w1", up.Src) // now in the note: readable for another writer
	if code != 200 || m["backend"] != "claude" || !strings.Contains(m["markdown"].(string), "Back squat") {
		t.Fatalf("on Claude Code = %d %v", code, m)
	}
	if imgs, _ := job["images"].([]any); len(imgs) != 1 || job["model"] != "claude-haiku-5-5" {
		t.Errorf("runner job = %v", job["model"])
	}
	// The trainer isn't the owner: the API.
	if _, m := read(trainer, "w1", up.Src); m["backend"] != "api" {
		t.Errorf("trainer's backend = %v", m["backend"])
	}

	// A photo without a workout.
	vision.mu.Lock()
	vision.answer = "NO_WORKOUT"
	vision.mu.Unlock()
	if code, m := read(trainer, "w1", up.Src); code != 422 || m["error"] != "no_workout" {
		t.Errorf("no workout = %d %v", code, m)
	}
}
