package ai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func parse(t *testing.T, file string) []RunEvent {
	t.Helper()
	f, err := os.Open("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var p Parser
	var out []RunEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		out = append(out, p.Feed(sc.Bytes())...)
	}
	return out
}

func TestParserRecordedRun(t *testing.T) {
	evs := parse(t, "claude-ok.ndjson")
	var kinds []string
	var text string
	for _, e := range evs {
		if len(kinds) == 0 || kinds[len(kinds)-1] != e.Kind {
			kinds = append(kinds, e.Kind)
		}
		if e.Kind == "text" {
			text += e.Text
		}
	}
	if strings.Join(kinds, " ") != "tool text done" {
		t.Fatalf("kinds = %v", kinds)
	}
	if evs[0].Tool != "sleep" || !strings.Contains(string(evs[0].Args), "2026-11-19") {
		t.Errorf("tool = %+v", evs[0])
	}
	last := evs[len(evs)-1]
	if last.Model != "claude-haiku-5-5" || last.Text != text || !strings.HasPrefix(text, "You slept") {
		t.Errorf("done = %+v, streamed %q", last, text)
	}
}

func TestParserLimit(t *testing.T) {
	evs := parse(t, "claude-limit.ndjson")
	if len(evs) != 1 || evs[0].Kind != "limit" || evs[0].Until != 1795194000 {
		t.Fatalf("events = %+v", evs)
	}
	// Only the text: the closest reset of a window that isn't "allowed".
	var p Parser
	p.Feed([]byte(`{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"status":"allowed_warning","resetsAt":100}}}}`))
	if evs := p.Feed([]byte(`{"type":"result","is_error":true,"result":"Claude usage limit reached."}`)); evs[0].Kind != "limit" || evs[0].Until != 100 {
		t.Errorf("text-only limit = %+v", evs)
	}
	// Any other failure is an error.
	var q Parser
	if evs := q.Feed([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}`)); evs[0].Kind != "error" || evs[0].Message != "error_during_execution" {
		t.Errorf("error = %+v", evs)
	}
}

func TestRunnerClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "no", 401)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			fmt.Fprint(w, `{"version":"2.1.329 (Claude Code)","token":true}`)
		case "/run":
			if r.ContentLength > 0 {
				var buf [512]byte
				n, _ := r.Body.Read(buf[:])
				if strings.Contains(string(buf[:n]), "limit") {
					fmt.Fprintln(w, `{"kind":"limit","until":1795194000}`)
					return
				}
			}
			fmt.Fprintln(w, `{"kind":"tool","tool":"sleep","args":{"from":"2026-11-19"}}`)
			fmt.Fprintln(w, `{"kind":"text","text":"Seven "}`)
			fmt.Fprintln(w, `{"kind":"text","text":"hours."}`)
			fmt.Fprintln(w, `{"kind":"done","text":"hours.","model":"claude-opus-5-5"}`)
		}
	}))
	defer srv.Close()
	r := Runner{URL: srv.URL, Key: "k"}
	if v, err := r.Health(context.Background()); err != nil || !strings.Contains(v, "2.1.329") {
		t.Errorf("health = %q %v", v, err)
	}
	if _, err := (Runner{URL: srv.URL, Key: "wrong"}).Health(context.Background()); !errors.Is(err, ErrRunnerDown) {
		t.Errorf("wrong key: %v", err)
	}
	var got []Event
	ans, err := r.Ask(context.Background(), RunRequest{Prompt: "q", Model: "opus"}, func(e Event) { got = append(got, e) })
	if err != nil || ans.Text != "Seven hours." || ans.Model != "claude-code/claude-opus-5-5" || len(ans.Tools) != 1 || len(got) != 3 {
		t.Fatalf("ask = %+v %v, events %v", ans, err, got)
	}
	_, err = r.Ask(context.Background(), RunRequest{Prompt: "limit", Model: "opus"}, func(Event) {})
	var le *LimitError
	if !errors.As(err, &le) || !le.Until.Equal(time.Unix(1795194000, 0)) {
		t.Errorf("limit = %v", err)
	}
}
