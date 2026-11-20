package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"eumetria/internal/ai"
	"eumetria/internal/auth"
	"eumetria/internal/settings"
)

// fakeClaude streams Messages API answers: the first request of each
// question calls the phases tool, the next one answers in text. It keeps
// every request body.
type fakeClaude struct {
	mu       sync.Mutex
	requests []map[string]any
	status   int // answer with this HTTP status instead (e.g. 400)
}

func sse(w io.Writer, event, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data) }

func (f *fakeClaude) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var body map[string]any
	json.NewDecoder(req.Body).Decode(&body)
	f.mu.Lock()
	f.requests = append(f.requests, body)
	status := f.status
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`)
		return
	}
	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	toolTurn := false
	if blocks, ok := last["content"].([]any); ok && len(blocks) > 0 {
		toolTurn = blocks[0].(map[string]any)["type"] == "tool_result"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	sse(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":1000,"output_tokens":1,"cache_read_input_tokens":500}}}`)
	if !toolTurn {
		sse(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"phases","input":{}}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)
		sse(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`)
	} else {
		sse(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"You are "}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"in a cut."}}`)
		sse(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":30}}`)
	}
	sse(w, "message_stop", `{"type":"message_stop"}`)
}

// events reads a server-sent event stream into (event, data) pairs.
func events(body string) [][2]string {
	var out [][2]string
	for _, chunk := range strings.Split(body, "\n\n") {
		var ev, data string
		for _, line := range strings.Split(chunk, "\n") {
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				ev = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
			}
		}
		if ev != "" {
			out = append(out, [2]string{ev, data})
		}
	}
	return out
}

func TestCoachAsk(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	fake := &fakeClaude{}
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)
	coachAPIURL = api.URL
	t.Cleanup(func() { coachAPIURL = "" })

	admin := e.signIn(t, "patrick", adminPassword, false)
	viewer := e.signIn(t, "anna", viewerPassword, false)
	ask := func(c *http.Client, thread int64, text string) (*http.Response, string) {
		b, _ := json.Marshal(map[string]any{"thread": thread, "text": text})
		return e.do(t, c, "POST", "/api/coach/ask", string(b), sameSite)
	}

	// A viewer's role lacks use.coach; admins have every scope.
	if res, _ := e.do(t, viewer, "GET", "/api/coach", ""); res.StatusCode != 403 {
		t.Errorf("viewer = %d, want 403", res.StatusCode)
	}
	// Off until it's switched on with a key.
	_, body := e.do(t, admin, "GET", "/api/coach", "")
	if !strings.Contains(body, `"backend":"off"`) || !strings.Contains(body, `"ready":false`) {
		t.Errorf("coach before setup = %s", body)
	}
	if res, _ := ask(admin, 0, "How am I doing?"); res.StatusCode != 409 {
		t.Errorf("ask while off = %d, want 409", res.StatusCode)
	}
	if err := e.runner.settings.Save(ctx, map[string]string{"coach": "api", "anthropic_key": "sk-test"}, nil); err != nil {
		t.Fatal(err)
	}
	if res, _ := ask(admin, 0, "   "); res.StatusCode != 400 {
		t.Errorf("empty question = %d, want 400", res.StatusCode)
	}

	res, body := ask(admin, 0, "What phase am I in?")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("ask = %d %s", res.StatusCode, body)
	}
	evs := events(body)
	var kinds []string
	var text string
	var done map[string]any
	var thread int64
	for _, ev := range evs {
		kinds = append(kinds, ev[0])
		switch ev[0] {
		case "text":
			var x struct{ Text string }
			json.Unmarshal([]byte(ev[1]), &x)
			text += x.Text
		case "done":
			json.Unmarshal([]byte(ev[1]), &done)
		case "thread":
			var x struct{ ID int64 }
			json.Unmarshal([]byte(ev[1]), &x)
			thread = x.ID
		}
	}
	if strings.Join(kinds, " ") != "thread tool text text done" || text != "You are in a cut." {
		t.Fatalf("events = %v, text %q", kinds, text)
	}
	if done["status"] != "done" || done["backend"] != "api" || done["model"] != "claude-sonnet-5-5" || done["cost_usd"].(float64) <= 0 {
		t.Errorf("done = %v", done)
	}

	// What was sent: the fixed instructions cached, today's date with the
	// question, the tools of the admin's scopes, the coach model.
	fake.mu.Lock()
	first := fake.requests[0]
	n := len(fake.requests)
	fake.mu.Unlock()
	if n != 2 || first["model"] != "claude-sonnet-5-5" || first["cache_control"] == nil {
		t.Errorf("requests = %d, first %v", n, first["model"])
	}
	sys := first["system"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(sys["text"].(string), "You are the coach in Eumetria") || sys["cache_control"] == nil {
		t.Errorf("system = %v", sys)
	}
	q := first["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(q, "Today is ") || !strings.HasSuffix(q, "What phase am I in?") {
		t.Errorf("question sent = %q", q)
	}
	if tools := first["tools"].([]any); len(tools) != 12 {
		t.Errorf("%d tools sent, want 12", len(tools))
	}

	// A follow-up sends the earlier turns along, as they were.
	ask(admin, thread, "And before?")
	fake.mu.Lock()
	third := fake.requests[2]
	fake.mu.Unlock()
	if msgs := third["messages"].([]any); len(msgs) != 5 { // question, tool call, tool result, answer, new question
		t.Errorf("follow-up sent %d messages, want 5", len(msgs))
	}

	// The thread is the admin's alone.
	_, body = e.do(t, admin, "GET", fmt.Sprintf("/api/coach/threads/%d", thread), "")
	var th struct{ Messages []map[string]any }
	json.Unmarshal([]byte(body), &th)
	if len(th.Messages) != 4 || th.Messages[1]["text"] != "You are in a cut." || th.Messages[1]["tools"] == nil {
		t.Errorf("thread = %s", body)
	}
	e.store.SaveRole(ctx, "Coached", []string{auth.ViewNutrition, auth.UseCoach})
	e.store.CreateUser(ctx, "bea", viewerPassword+"!", "Coached")
	bea := e.signIn(t, "bea", viewerPassword+"!", false)
	if res, _ := e.do(t, bea, "GET", fmt.Sprintf("/api/coach/threads/%d", thread), ""); res.StatusCode != 404 {
		t.Errorf("someone else's thread = %d, want 404", res.StatusCode)
	}
	if res, _ := e.do(t, bea, "DELETE", fmt.Sprintf("/api/coach/threads/%d", thread), "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("deleting someone else's thread = %d, want 404", res.StatusCode)
	}
	// Bea's role sees nutrition only: her coach gets those tools only.
	ask(bea, 0, "Hi")
	fake.mu.Lock()
	beaTools := fake.requests[len(fake.requests)-2]["tools"].([]any)
	fake.mu.Unlock()
	var names []string
	for _, x := range beaTools {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if strings.Join(names, " ") != "overview daily_metrics body phases" {
		t.Errorf("bea's tools = %v", names)
	}

	// No credit: the answer fails and says why.
	fake.mu.Lock()
	fake.status = 400
	fake.mu.Unlock()
	_, body = ask(admin, 0, "Again?")
	if !strings.Contains(body, `"status":"failed"`) || !strings.Contains(body, `"error":"billing"`) {
		t.Errorf("billing failure = %s", body)
	}

	if res, _ := e.do(t, admin, "DELETE", fmt.Sprintf("/api/coach/threads/%d", thread), "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("delete = %d", res.StatusCode)
	}
	if res, _ := e.do(t, admin, "GET", fmt.Sprintf("/api/coach/threads/%d", thread), ""); res.StatusCode != 404 {
		t.Errorf("deleted thread = %d, want 404", res.StatusCode)
	}
}

// fakeRunner answers like the runner: it checks the job's MCP token on the
// real /api/mcp, then answers, or says the limit is reached.
type fakeRunner struct {
	mu      sync.Mutex
	limit   bool
	jobs    []map[string]any
	mcp     *http.Client
	mcpCode int
}

func (f *fakeRunner) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Authorization") != "Bearer runner-key" {
		http.Error(w, "no", 401)
		return
	}
	if req.URL.Path == "/healthz" {
		fmt.Fprint(w, `{"version":"2.1.329 (Claude Code)","token":true}`)
		return
	}
	var job map[string]any
	json.NewDecoder(req.Body).Decode(&job)
	f.mu.Lock()
	f.jobs = append(f.jobs, job)
	limit := f.limit
	f.mu.Unlock()
	if limit {
		fmt.Fprintln(w, `{"kind":"limit","until":`+fmt.Sprint(time.Now().Add(2*time.Hour).Unix())+`}`)
		return
	}
	// The token must work on /api/mcp, for this one job.
	mreq, _ := http.NewRequest("POST", job["mcp_url"].(string), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"phases","arguments":{}}}`))
	mreq.Header.Set("Authorization", "Bearer "+job["mcp_token"].(string))
	mreq.Header.Set("Content-Type", "application/json")
	if res, err := f.mcp.Do(mreq); err == nil {
		f.mu.Lock()
		f.mcpCode = res.StatusCode
		f.mu.Unlock()
		res.Body.Close()
	}
	fmt.Fprintln(w, `{"kind":"tool","tool":"phases","args":{}}`)
	fmt.Fprintln(w, `{"kind":"text","text":"From Claude Code."}`)
	fmt.Fprintln(w, `{"kind":"done","text":"From Claude Code.","model":"claude-opus-5-5"}`)
}

func TestCoachClaudeCode(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	fakeAPI := &fakeClaude{}
	api := httptest.NewServer(fakeAPI)
	t.Cleanup(api.Close)
	runner := &fakeRunner{mcp: e.srv.Client()}
	rs := httptest.NewServer(runner)
	t.Cleanup(rs.Close)
	coachAPIURL = api.URL
	coachRunner = func(v settings.Values) ai.Runner { return ai.Runner{URL: rs.URL, Key: "runner-key"} }
	coachWorkerEvery = 20 * time.Millisecond
	t.Cleanup(func() {
		coachAPIURL = ""
		coachRunner = func(v settings.Values) ai.Runner { return ai.Runner{URL: v.RunnerURL, Key: v.RunnerKey} }
		coachWorkerEvery = 30 * time.Second
	})
	if err := e.runner.settings.Save(ctx, map[string]string{"coach": "claude", "anthropic_key": "sk-test",
		"runner_mcp_url": e.srv.URL + "/api/mcp", "claude_model": "opus"}, nil); err != nil {
		t.Fatal(err)
	}
	patrick := e.signIn(t, "patrick", adminPassword, false) // the owner: the oldest admin
	ask := func(c *http.Client, thread int64, text string) map[string]any {
		b, _ := json.Marshal(map[string]any{"thread": thread, "text": text})
		res, body := e.do(t, c, "POST", "/api/coach/ask", string(b), sameSite)
		if res.StatusCode != 200 {
			t.Fatalf("ask = %d %s", res.StatusCode, body)
		}
		var done map[string]any
		for _, ev := range events(body) {
			if ev[0] == "done" {
				json.Unmarshal([]byte(ev[1]), &done)
			}
		}
		return done
	}

	_, home := e.do(t, patrick, "GET", "/api/coach", "")
	if !strings.Contains(home, `"uses":"claude"`) {
		t.Fatalf("home = %s", home)
	}
	done := ask(patrick, 0, "What phase am I in?")
	if done["backend"] != "claude" || done["text"] != "From Claude Code." || done["model"] != "claude-code/claude-opus-5-5" {
		t.Fatalf("done = %v", done)
	}
	runner.mu.Lock()
	job := runner.jobs[0]
	code := runner.mcpCode
	runner.mu.Unlock()
	if code != 200 || job["model"] != "opus" || !strings.HasPrefix(job["system"].(string), "You are the coach") || !strings.Contains(job["prompt"].(string), "What phase am I in?") {
		t.Errorf("job = %v, mcp answered %d", job, code)
	}
	// The job's token is gone afterwards.
	owner, _ := e.store.UserByName(ctx, "patrick")
	if toks, _ := e.store.APITokens(ctx, owner); len(toks) != 0 {
		t.Errorf("tokens left: %v", toks)
	}
	if _, _, err := e.store.APIToken(ctx, job["mcp_token"].(string)); err == nil {
		t.Error("the job's token still works")
	}

	// The limit: the question waits; the next one waits without asking.
	runner.mu.Lock()
	runner.limit = true
	runner.mu.Unlock()
	done = ask(patrick, 0, "And my sleep?")
	if done["status"] != "queued" || done["queued_until"] == nil {
		t.Fatalf("at the limit = %v", done)
	}
	n := len(runner.jobs)
	ask(patrick, 0, "And my food?")
	if len(runner.jobs) != n {
		t.Error("asked the runner again although the limit is reached")
	}
	_, home = e.do(t, patrick, "GET", "/api/coach", "")
	if !strings.Contains(home, `"limit_until"`) {
		t.Errorf("home at the limit = %s", home)
	}

	// Someone else with the coach scope is answered on the API.
	e.store.SaveRole(ctx, "Coached", []string{auth.ViewNutrition, auth.UseCoach})
	e.store.CreateUser(ctx, "bea", viewerPassword+"!", "Coached")
	bea := e.signIn(t, "bea", viewerPassword+"!", false)
	if done := ask(bea, 0, "Hi"); done["backend"] != "api" || done["status"] != "done" {
		t.Errorf("bea = %v", done)
	}

	// After the reset the worker answers both waiting questions.
	runner.mu.Lock()
	runner.limit = false
	runner.mu.Unlock()
	e.now = e.now.Add(3 * time.Hour)
	e.do(t, patrick, "GET", "/api/coach", "") // starts the worker
	deadline := time.Now().Add(5 * time.Second)
	for {
		var answered int
		e.store.DB.QueryRowContext(ctx, `SELECT count(*) FROM coach_messages m JOIN coach_threads t ON t.id = m.thread_id
			WHERE t.user_id = ? AND m.role = 'assistant' AND m.status = 'done'`, owner.ID).Scan(&answered)
		if answered == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker answered %d of 3", answered)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
