package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// fakeProc replays a recorded stream-json run.
type fakeProc struct{ out io.Reader }

func (p fakeProc) Output() io.Reader { return p.out }
func (fakeProc) Wait() error         { return nil }
func (fakeProc) ErrTail() string     { return "" }

func TestRun(t *testing.T) {
	rec, err := os.ReadFile("../../internal/ai/testdata/claude-ok.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs, gotEnv []string
	var gotStdin, gotDir string
	s := &server{key: strings.Repeat("k", 32), token: true, slots: make(chan struct{}, 1)}
	s.start = func(ctx context.Context, dir string, args, env []string, stdin string) (proc, error) {
		gotArgs, gotEnv, gotStdin, gotDir = args, env, stdin, dir
		cfg, _ := os.ReadFile(args[slices.Index(args, "--mcp-config")+1])
		if !strings.Contains(string(cfg), `"Authorization":"Bearer eum_job"`) {
			t.Errorf("mcp config = %s", cfg)
		}
		return fakeProc{strings.NewReader(string(rec))}, nil
	}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	post := func(key, body string) (*http.Response, string) {
		req, _ := http.NewRequest("POST", srv.URL+"/run", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	if res, _ := post("wrong", `{}`); res.StatusCode != 401 {
		t.Errorf("wrong key = %d", res.StatusCode)
	}
	if res, _ := post(s.key, `{"prompt":"x","model":"gpt"}`); res.StatusCode != 400 {
		t.Errorf("unknown model = %d", res.StatusCode)
	}
	res, body := post(s.key, `{"system":"Be a coach.","prompt":"How did I sleep?","model":"opus","mcp_url":"http://eumetria:8080/api/mcp","mcp_token":"eum_job"}`)
	if res.StatusCode != 200 || !strings.Contains(body, `{"kind":"tool","tool":"sleep"`) || !strings.Contains(body, `"kind":"done"`) {
		t.Fatalf("run = %d %s", res.StatusCode, body)
	}
	// No built-in tools, no settings files, only Eumetria's MCP tools.
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-p", "--tools  --restricted", "--strict-mcp-config", "--allowedTools mcp__eumetria__*", "--model opus", "--no-session-persistence", "--system-prompt Be a coach."} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if gotStdin != "How did I sleep?" || !strings.HasSuffix(gotDir, "/work") || !slices.ContainsFunc(gotEnv, func(e string) bool { return strings.HasPrefix(e, "HOME=") && strings.HasSuffix(e, "/home") }) {
		t.Errorf("stdin %q, dir %q, env %v", gotStdin, gotDir, gotEnv)
	}
	if _, err := os.Stat(strings.TrimSuffix(gotDir, "/work")); !os.IsNotExist(err) {
		t.Errorf("the job's directory is still there: %v", err)
	}
	// The env holds only what claude needs: no RUNNER_KEY.
	if slices.ContainsFunc(gotEnv, func(e string) bool { return strings.HasPrefix(e, "RUNNER_KEY") }) {
		t.Error("RUNNER_KEY reached claude")
	}
}
