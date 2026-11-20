package ai

// The Claude Code backend (phase 12b): Eumetria sends a RunRequest to the
// runner container (cmd/claude-runner), which runs the unmodified
// `claude -p` with your subscription and only Eumetria's MCP tools, and
// streams RunEvents back as JSON lines. Parser turns Claude Code's
// stream-json output into those events; it lives here so both sides and
// the tests share it.
//
// The reset time of the usage limit comes from Claude Code's
// rate_limit_event (unifiedWindows.*.resetsAt, Unix seconds), recorded on
// 2026-11-20 (testdata/claude-ok.ndjson). What a run that hits the limit
// prints is not recorded yet: a window whose status is "rejected", or a
// failed result that talks about a usage limit, counts as one.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// RunRequest is one job for the runner.
type RunRequest struct {
	System   string `json:"system"`
	Prompt   string `json:"prompt"`
	Model    string `json:"model"`               // a model's name (claude-opus-5-5) or alias (opus)
	Effort   string `json:"effort,omitempty"`    // low … max; empty: Claude Code's default
	MCPURL   string `json:"mcp_url,omitempty"`   // empty: no tools
	MCPToken string `json:"mcp_token,omitempty"` // a short-lived "mcp" API token
	Timeout  int    `json:"timeout,omitempty"`   // seconds; 0: 300
	// Images go with the prompt, before it: JPEGs (base64 in JSON).
	Images [][]byte `json:"images,omitempty"`
}

// RunEvent is one line of the runner's answer.
type RunEvent struct {
	Kind    string          `json:"kind"` // text, tool, done, limit, error
	Text    string          `json:"text,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Until   int64           `json:"until,omitempty"` // limit: when it resets, Unix seconds; 0 unknown
	Model   string          `json:"model,omitempty"` // done: the model that answered
	Message string          `json:"message,omitempty"`
}

// MCPPrefix is what Claude Code puts before the names of Eumetria's tools.
const MCPPrefix = "mcp__eumetria__"

// Parser reads Claude Code's stream-json output line by line.
type Parser struct {
	model    string
	windows  map[string]window
	finished bool
}

type window struct {
	Status   string `json:"status"`
	ResetsAt int64  `json:"resetsAt"`
}

// Feed takes one line of output and answers the events it makes.
func (p *Parser) Feed(line []byte) []RunEvent {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var m struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Model   string `json:"model"`
		Event   struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		} `json:"event"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
		RateLimit struct {
			Status         string            `json:"status"`
			ResetsAt       int64             `json:"resetsAt"`
			RateLimitType  string            `json:"rateLimitType"`
			UnifiedWindows map[string]window `json:"unifiedWindows"`
		} `json:"rate_limit_info"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	switch m.Type {
	case "system":
		if m.Subtype == "init" && m.Model != "" {
			p.model = m.Model
		}
	case "stream_event":
		if m.Event.Type == "content_block_delta" && m.Event.Delta.Type == "text_delta" && m.Event.Delta.Text != "" {
			return []RunEvent{{Kind: "text", Text: m.Event.Delta.Text}}
		}
	case "assistant":
		var out []RunEvent
		for _, b := range m.Message.Content {
			if b.Type == "tool_use" {
				out = append(out, RunEvent{Kind: "tool", Tool: strings.TrimPrefix(b.Name, MCPPrefix), Args: b.Input})
			}
		}
		return out
	case "rate_limit_event":
		if p.windows == nil {
			p.windows = map[string]window{}
		}
		for k, w := range m.RateLimit.UnifiedWindows {
			p.windows[k] = w
		}
		if t := m.RateLimit.RateLimitType; t != "" {
			p.windows[t] = window{Status: m.RateLimit.Status, ResetsAt: m.RateLimit.ResetsAt}
		}
	case "result":
		p.finished = true
		if !m.IsError && m.Subtype == "success" {
			return []RunEvent{{Kind: "done", Text: strings.TrimSpace(m.Result), Model: p.model}}
		}
		if until, hit := p.limit(m.Result); hit {
			return []RunEvent{{Kind: "limit", Until: until, Message: clip(m.Result, 300)}}
		}
		return []RunEvent{{Kind: "error", Message: clip(firstNonEmpty(m.Result, m.Subtype), 300)}}
	}
	return nil
}

// Finished reports whether a result line came.
func (p *Parser) Finished() bool { return p.finished }

var limitText = regexp.MustCompile(`(?i)(usage|rate|session|weekly)[^.]*limit|limit[^.]*reached|hit your limit`)

// limit reports whether a failed run hit the usage limit, and until when.
func (p *Parser) limit(text string) (int64, bool) {
	var until int64
	rejected := false
	for _, w := range p.windows {
		if w.Status == "rejected" {
			rejected = true
			until = max(until, w.ResetsAt)
		}
	}
	if !rejected && !limitText.MatchString(text) {
		return 0, false
	}
	if until == 0 {
		// The text alone: the latest known reset of a window that is close.
		for _, w := range p.windows {
			if w.Status != "allowed" {
				until = max(until, w.ResetsAt)
			}
		}
	}
	return until, true
}

func clip(s string, most int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > most {
		return string(r[:most]) + "…"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// LimitError is the runner saying the subscription's limit is reached.
// Until is when it resets; zero when unknown.
type LimitError struct{ Until time.Time }

func (e *LimitError) Error() string {
	if e.Until.IsZero() {
		return "claude code: usage limit reached"
	}
	return "claude code: usage limit reached until " + e.Until.Format(time.RFC3339)
}

// ErrRunnerDown is the runner not reachable or not set up.
var ErrRunnerDown = errors.New("claude code runner not reachable")

// Runner is how Eumetria reaches the runner container.
type Runner struct {
	URL, Key string
	Client   *http.Client
}

func (r Runner) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return http.DefaultClient
}

// Ready reports whether a runner is set up (address and key).
func (r Runner) Ready() bool { return r.URL != "" && r.Key != "" }

// Health asks the runner whether it runs and can sign in; it answers the
// Claude Code version.
func (r Runner) Health(ctx context.Context) (string, error) {
	if !r.Ready() {
		return "", ErrRunnerDown
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.URL, "/")+"/healthz", nil)
	req.Header.Set("Authorization", "Bearer "+r.Key)
	res, err := r.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRunnerDown, err)
	}
	defer res.Body.Close()
	var h struct {
		Version string `json:"version"`
		Token   bool   `json:"token"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&h) != nil {
		return "", fmt.Errorf("%w: HTTP %d", ErrRunnerDown, res.StatusCode)
	}
	if !h.Token {
		return h.Version, errors.New("the runner has no CLAUDE_CODE_OAUTH_TOKEN")
	}
	return h.Version, nil
}

// Ask runs req on the runner, sending emit the text and tool calls as they
// come. A reached limit is a *LimitError.
func (r Runner) Ask(ctx context.Context, req RunRequest, emit func(Event)) (*Answer, error) {
	if !r.Ready() {
		return nil, ErrRunnerDown
	}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.URL, "/")+"/run", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+r.Key)
	hreq.Header.Set("Content-Type", "application/json")
	res, err := r.client().Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRunnerDown, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return nil, fmt.Errorf("claude code runner: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	ans := &Answer{Model: "claude-code/" + req.Model}
	var text strings.Builder
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var ev RunEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Kind {
		case "text":
			text.WriteString(ev.Text)
			emit(Event{Kind: "text", Text: ev.Text})
		case "tool":
			args := ev.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			ans.Tools = append(ans.Tools, ToolCall{Name: ev.Tool, Args: args})
			emit(Event{Kind: "tool", Tool: ev.Tool, Args: args})
			// Text before a tool call belongs to the answer too.
			if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
				text.WriteString("\n\n")
				emit(Event{Kind: "text", Text: "\n\n"})
			}
		case "done":
			// What streamed is what the page showed; the result line holds
			// only the last message's text.
			ans.Text = strings.TrimSpace(text.String())
			if ans.Text == "" {
				ans.Text = ev.Text
			}
			if ev.Model != "" {
				ans.Model = "claude-code/" + ev.Model
			}
			return ans, nil
		case "limit":
			le := &LimitError{}
			if ev.Until > 0 {
				le.Until = time.Unix(ev.Until, 0)
			}
			return ans, le
		case "error":
			return ans, fmt.Errorf("claude code: %s", ev.Message)
		}
	}
	if err := sc.Err(); err != nil {
		return ans, fmt.Errorf("claude code runner: %w", err)
	}
	return ans, errors.New("claude code runner: the answer ended early")
}
