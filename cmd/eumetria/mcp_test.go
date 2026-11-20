package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
)

// rpc posts one JSON-RPC message to /api/mcp with token.
func (e *testEnv) rpc(t *testing.T, token, body string) (int, map[string]any) {
	t.Helper()
	res, out := e.do(t, e.srv.Client(), "POST", "/api/mcp", body, bearer(token))
	var m map[string]any
	if out != "" && res.StatusCode == 200 {
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("answer %q: %v", out, err)
		}
	}
	return res.StatusCode, m
}

func toolNames(t *testing.T, m map[string]any) []string {
	t.Helper()
	var names []string
	for _, x := range m["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

// callText calls a tool and returns its text and whether it is an error.
func (e *testEnv) callTool(t *testing.T, token, name, args string) (string, bool) {
	t.Helper()
	code, m := e.rpc(t, token, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	if code != 200 || m["error"] != nil {
		t.Fatalf("tools/call %s = %d %v", name, code, m)
	}
	r := m["result"].(map[string]any)
	return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
}

func TestMCPRoundTrip(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	patrick, _ := e.store.UserByName(ctx, "patrick")
	tok, err := e.store.CreateAPIToken(ctx, patrick, "claude", []string{auth.TokenMCP}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	code, m := e.rpc(t, tok, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if code != 200 {
		t.Fatalf("initialize = %d", code)
	}
	r := m["result"].(map[string]any)
	if r["protocolVersion"] != "2025-06-18" || r["serverInfo"].(map[string]any)["name"] != "eumetria" || r["capabilities"].(map[string]any)["tools"] == nil {
		t.Errorf("initialize = %v", r)
	}
	if code, m := e.rpc(t, tok, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`); code != 200 || m["result"].(map[string]any)["protocolVersion"] != mcpVersions[0] {
		t.Errorf("initialize with an unknown version = %d %v, want the newest", code, m)
	}
	// Notifications get 202 and no body.
	if code, _ := e.rpc(t, tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Errorf("notification = %d, want 202", code)
	}
	if _, m := e.rpc(t, tok, `{"jsonrpc":"2.0","id":"p","method":"ping"}`); m["id"] != "p" || m["error"] != nil {
		t.Errorf("ping = %v", m)
	}
	if _, m := e.rpc(t, tok, `{"jsonrpc":"2.0","id":2,"method":"resources/list"}`); m["error"].(map[string]any)["code"] != float64(rpcNoMethod) {
		t.Errorf("unknown method = %v", m)
	}
	if res, _ := e.do(t, e.srv.Client(), "POST", "/api/mcp", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, bearer(tok)); res.StatusCode != 200 {
		t.Errorf("batch = %d", res.StatusCode)
	}

	_, m = e.rpc(t, tok, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	names := toolNames(t, m)
	if strings.Join(names, " ") != "overview daily_metrics entries food_sources workouts sleep body checkins lifts sessions wods phases" {
		t.Errorf("an admin's tools = %v", names)
	}

	text, isErr := e.callTool(t, tok, "entries", `{"date":"2026-10-20"}`)
	if isErr || !strings.Contains(text, `"name":"Skyr"`) {
		t.Errorf("entries = %v %s", isErr, text)
	}
	text, isErr = e.callTool(t, tok, "workouts", `{"from":"2026-10-01","to":"2026-10-31"}`)
	if isErr || !strings.Contains(text, `"type":"RUNNING"`) || !strings.Contains(text, `"elapsed_min":60`) {
		t.Errorf("workouts = %v %s", isErr, text)
	}
	text, isErr = e.callTool(t, tok, "daily_metrics", `{"from":"2026-10-20","to":"2026-10-20","fields":["energy","steps"]}`)
	if isErr || text != `{"days":[{"date":"2026-10-20","energy":1800,"steps":9000}],"from":"2026-10-20","to":"2026-10-20"}` {
		t.Errorf("daily_metrics = %v %s", isErr, text)
	}
	// Bad arguments are the model's to fix: a tool error, not a protocol one.
	if text, isErr := e.callTool(t, tok, "daily_metrics", `{"from":"20.10.2026"}`); !isErr || !strings.Contains(text, "not a date") {
		t.Errorf("bad date = %v %s", isErr, text)
	}
	if text, isErr := e.callTool(t, tok, "phases", `{"x":1}`); !isErr || !strings.Contains(text, "unknown field") {
		t.Errorf("unknown argument = %v %s", isErr, text)
	}
	if _, m := e.rpc(t, tok, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`); m["error"].(map[string]any)["code"] != float64(rpcInvalidParams) {
		t.Errorf("unknown tool = %v", m)
	}

	// Each call is in the audit log, with its arguments.
	entries, err := e.store.AuditLog(ctx, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, a := range entries {
		if a.Action == "mcp_call" {
			calls = append(calls, a.Detail)
		}
	}
	if len(calls) != 6 || !slicesContains(calls, `entries {"date":"2026-10-20"}`) {
		t.Errorf("audited calls = %q", calls)
	}
}

func slicesContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestMCPAccess(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	patrick, _ := e.store.UserByName(ctx, "patrick")
	coach, _ := e.store.UserByName(ctx, "coach")
	mcpTok, _ := e.store.CreateAPIToken(ctx, patrick, "claude", []string{auth.TokenMCP}, time.Time{})
	readTok, _ := e.store.CreateAPIToken(ctx, patrick, "export", []string{"read"}, time.Time{})
	coachTok, _ := e.store.CreateAPIToken(ctx, coach, "claude", []string{auth.TokenMCP}, time.Time{})
	plain := e.srv.Client()
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	// Only an mcp token gets in; an mcp token gets nowhere else.
	if code, _ := e.rpc(t, readTok, ping); code != 403 {
		t.Errorf("read token on /api/mcp = %d, want 403", code)
	}
	if res, _ := e.do(t, plain, "GET", "/api/stats", "", bearer(mcpTok)); res.StatusCode != 403 {
		t.Errorf("mcp token on /api/stats = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "GET", "/api/me", "", bearer(mcpTok)); res.StatusCode != 403 {
		t.Errorf("mcp token on /api/me = %d, want 403", res.StatusCode)
	}
	if code, _ := e.rpc(t, "eum_wrong", ping); code != 401 {
		t.Errorf("wrong token = %d, want 401", code)
	}
	browser := e.signIn(t, "patrick", adminPassword, false)
	if res, _ := e.do(t, browser, "POST", "/api/mcp", ping, sameSite); res.StatusCode != 403 {
		t.Errorf("session on /api/mcp = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "POST", "/api/mcp", ping, bearer(mcpTok), header("Origin", "https://evil.example")); res.StatusCode != 403 {
		t.Errorf("foreign Origin = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "POST", "/api/mcp", ping, bearer(mcpTok), header("MCP-Protocol-Version", "1999-01-01")); res.StatusCode != 400 {
		t.Errorf("unknown protocol version header = %d, want 400", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "GET", "/api/mcp", "", bearer(mcpTok)); res.StatusCode != 405 {
		t.Errorf("GET = %d, want 405", res.StatusCode)
	}

	// The coach role sees nutrition and foods only: the tools follow.
	_, m := e.rpc(t, coachTok, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if names := strings.Join(toolNames(t, m), " "); names != "overview daily_metrics entries food_sources body phases" {
		t.Errorf("coach's tools = %s", names)
	}
	if text, isErr := e.callTool(t, coachTok, "workouts", `{}`); !isErr || !strings.Contains(text, "may not see") {
		t.Errorf("workouts for the coach = %v %s", isErr, text)
	}
	if text, _ := e.callTool(t, coachTok, "daily_metrics", `{"from":"2026-10-20","to":"2026-10-20"}`); strings.Contains(text, "steps") || !strings.Contains(text, `"energy":1800`) {
		t.Errorf("coach's daily_metrics = %s", text)
	}

	// A revoked token stops at once.
	list, _ := e.store.APITokens(ctx, patrick)
	for _, k := range list {
		if k.Name == "claude" {
			e.store.DeleteAPIToken(ctx, patrick, k.ID)
		}
	}
	if code, _ := e.rpc(t, mcpTok, ping); code != 401 {
		t.Errorf("revoked token = %d, want 401", code)
	}
}

func TestMCPRateLimit(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	patrick, _ := e.store.UserByName(ctx, "patrick")
	tok, _ := e.store.CreateAPIToken(ctx, patrick, "claude", []string{auth.TokenMCP}, time.Time{})
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"phases","arguments":{}}}`
	for i := 0; i < mcpCallsPerMinute; i++ {
		if code, _ := e.rpc(t, tok, call); code != 200 {
			t.Fatalf("call %d = %d", i, code)
		}
	}
	if code, _ := e.rpc(t, tok, call); code != 429 {
		t.Errorf("call over the limit = %d, want 429", code)
	}
	// Listing isn't counted.
	if code, _ := e.rpc(t, tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 200 {
		t.Errorf("tools/list over the limit = %d", code)
	}
}
