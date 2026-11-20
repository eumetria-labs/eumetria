// claude-runner runs the unmodified Claude Code CLI for Eumetria, signed in
// with the owner's subscription (CLAUDE_CODE_OAUTH_TOKEN from
// `claude setup-token`). It lives in a container of its own
// (deploy/claude-runner) so Eumetria never holds that token, and serves
// only Eumetria, which sends RUNNER_KEY:
//
//	GET  /healthz  {version, token}: Claude Code's version, whether a token is set
//	POST /run      ai.RunRequest → JSON lines of ai.RunEvent
//
// Each job runs `claude -p` in an empty temporary home and working
// directory, with no built-in tools (--tools "", --restricted) and only
// Eumetria's MCP server (--strict-mcp-config), whose short-lived token the
// job brings. Nothing is kept after it: no sessions, no files.
package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"eumetria/internal/ai"
)

const (
	maxTimeout = 10 * time.Minute
	maxJobs    = 2 // at a time
)

type server struct {
	key     string
	token   bool
	claude  string // the program
	version string
	slots   chan struct{}
	// start starts the program; tests replace it.
	start func(ctx context.Context, dir string, args, env []string, stdin string) (proc, error)
}

// proc is a running claude: its output, and waiting for its end.
type proc interface {
	Output() io.Reader
	Wait() error
	ErrTail() string
}

func main() {
	addr := envOr("RUNNER_ADDR", ":8080")
	s := &server{
		key:    os.Getenv("RUNNER_KEY"),
		token:  os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "",
		claude: envOr("CLAUDE_BIN", "claude"),
		slots:  make(chan struct{}, maxJobs),
	}
	if len(s.key) < 32 {
		log.Fatal("RUNNER_KEY must be set, at least 32 characters (the same as in Eumetria's env file)")
	}
	if !s.token {
		log.Print("WARNING: CLAUDE_CODE_OAUTH_TOKEN isn't set: run `claude setup-token` and put it in the env file")
	}
	if out, err := exec.Command(s.claude, "--version").Output(); err == nil {
		s.version = strings.TrimSpace(string(out))
	}
	log.Printf("claude-runner on %s, %s", addr, s.version)
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.auth(s.health))
	mux.HandleFunc("POST /run", s.auth(s.runJob))
	return mux
}

func (s *server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"version": s.version, "token": s.token})
}

// A model is an alias or a model's name; nothing else reaches the command line.
var modelName = regexp.MustCompile(`^(opus|sonnet|haiku|fable|claude-[a-z0-9.-]{1,60})$`)

var efforts = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// args are the command line for a job.
func args(req ai.RunRequest, mcpFile string) []string {
	a := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--model", req.Model, "--tools", "", "--restricted", "--strict-mcp-config", "--no-session-persistence",
		"--system-prompt", req.System}
	if req.Effort != "" {
		a = append(a, "--effort", req.Effort)
	}
	if mcpFile != "" {
		a = append(a, "--mcp-config", mcpFile, "--allowedTools", ai.MCPPrefix+"*")
	}
	return a
}

func (s *server) runJob(w http.ResponseWriter, r *http.Request) {
	var req ai.RunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.Prompt == "" || !modelName.MatchString(req.Model) || !efforts[req.Effort] {
		http.Error(w, "bad request: {system, prompt, model, effort?, mcp_url?, mcp_token?}", http.StatusBadRequest)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "busy", http.StatusTooManyRequests)
		return
	}
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 || timeout > maxTimeout {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	var mu sync.Mutex
	send := func(ev ai.RunEvent) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(ev)
		w.Write(append(b, '\n'))
		if flusher != nil {
			flusher.Flush()
		}
	}

	// An empty home and working directory per job: no project, no settings,
	// no memory, nothing left behind.
	dir, err := os.MkdirTemp("", "job-")
	if err != nil {
		send(ai.RunEvent{Kind: "error", Message: "no temporary directory"})
		return
	}
	defer os.RemoveAll(dir)
	mcpFile := ""
	if req.MCPURL != "" {
		cfg := map[string]any{"mcpServers": map[string]any{"eumetria": map[string]any{
			"type": "http", "url": req.MCPURL, "headers": map[string]string{"Authorization": "Bearer " + req.MCPToken}}}}
		b, _ := json.Marshal(cfg)
		mcpFile = filepath.Join(dir, "mcp.json")
		if err := os.WriteFile(mcpFile, b, 0o600); err != nil {
			send(ai.RunEvent{Kind: "error", Message: "writing the MCP config failed"})
			return
		}
	}
	home := filepath.Join(dir, "home")
	work := filepath.Join(dir, "work")
	tmp := filepath.Join(dir, "tmp") // claude's own temporary files go too
	for _, d := range []string{home, work, tmp} {
		os.MkdirAll(d, 0o700)
	}
	env := []string{"HOME=" + home, "TMPDIR=" + tmp, "PATH=" + os.Getenv("PATH"), "CLAUDE_CODE_OAUTH_TOKEN=" + os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
		"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}

	start := s.start
	if start == nil {
		start = startClaude(s.claude)
	}
	cmd, err := start(ctx, work, args(req, mcpFile), env, req.Prompt)
	if err != nil {
		send(ai.RunEvent{Kind: "error", Message: "claude didn't start: " + err.Error()})
		return
	}
	var p ai.Parser
	out := bufio.NewReaderSize(cmd.Output(), 256<<10)
	for {
		line, err := out.ReadBytes('\n')
		for _, ev := range p.Feed(line) {
			send(ev)
		}
		if err != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	if !p.Finished() {
		msg := "claude ended without an answer"
		if ctx.Err() != nil {
			msg = "claude took too long"
		} else if waitErr != nil {
			msg = fmt.Sprintf("claude failed: %v", waitErr)
		}
		if tail := cmd.ErrTail(); tail != "" {
			log.Printf("job: %s: %s", msg, tail)
		}
		send(ai.RunEvent{Kind: "error", Message: msg})
	}
}

// startClaude starts the CLI with the prompt on stdin.
func startClaude(bin string) func(ctx context.Context, dir string, args, env []string, stdin string) (proc, error) {
	return func(ctx context.Context, dir string, args, env []string, stdin string) (proc, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Env = dir, env
		cmd.Stdin = strings.NewReader(stdin)
		p := &cliProc{cmd: cmd}
		cmd.Stderr = &p.stderr
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		p.out = out
		return p, cmd.Start()
	}
}

type cliProc struct {
	cmd    *exec.Cmd
	out    io.Reader
	stderr tail
}

func (p *cliProc) Output() io.Reader { return p.out }
func (p *cliProc) Wait() error       { return p.cmd.Wait() }
func (p *cliProc) ErrTail() string   { return p.stderr.String() }

// tail keeps the last 2 KB written to it.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}
