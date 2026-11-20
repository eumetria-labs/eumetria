// Package ai asks Claude on behalf of the coach chat. Phase 12a has one
// backend, the Anthropic API (this file): a tool loop over the Messages
// API that calls the coach tools in-process and streams the answer's text.
// Phase 12b adds the Claude Code runner behind the same Job and Event.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// Tool is one tool the model may call: its definition and how to run it.
// Run answers the result's text and whether it is an error the model
// should see as one.
type Tool struct {
	Name, Description string
	Schema            json.RawMessage // JSON Schema of the arguments, an object
	Run               func(ctx context.Context, args json.RawMessage) (string, bool)
}

// Job is one question.
type Job struct {
	APIKey   string
	Model    string
	Effort   string // low … max
	System   string // fixed instructions: they and the tools are cached
	History  []json.RawMessage
	Question string
	Tools    []Tool
	MaxTurns int // model requests at most (each tool round is one); 0: 12
	// BaseURL points the client elsewhere (tests).
	BaseURL string
}

// Event is what the page sees while an answer is written: a piece of
// text, or a tool call starting.
type Event struct {
	Kind string          `json:"kind"` // "text" or "tool"
	Text string          `json:"text,omitempty"`
	Tool string          `json:"tool,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ToolCall is a tool the answer used.
type ToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Usage adds up the tokens of every request of an answer.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
}

// Answer is a finished answer.
type Answer struct {
	Text  string
	Turns []json.RawMessage // the question and every turn after it, as sent
	Tools []ToolCall
	Model string
	Usage Usage
	Cost  float64
	Known bool // the price of Model is known (Cost counts)
}

// ErrRefused is Claude declining to answer; ErrTooLong an answer that
// needed more tool rounds than allowed or hit the token limit.
var (
	ErrRefused = errors.New("claude declined to answer")
	ErrTooLong = errors.New("the answer got too long")
)

// prices are USD per million input and output tokens; a cache read costs a
// tenth of an input token, writing to the cache 1.25 times one.
var prices = map[string][2]float64{
	"claude-opus-5-5":   {4, 20},
	"claude-sonnet-5-5": {2, 10},
	"claude-haiku-5-5":  {0.10, 0.50},
	"claude-fable-5-2":  {8, 40},
	"claude-fable-5-1":  {10, 50},
}

// Cost is what usage cost on model, and whether its price is known.
func Cost(model string, u Usage) (float64, bool) {
	p, ok := prices[model]
	if !ok {
		return 0, false
	}
	in := float64(u.Input) + 0.1*float64(u.CacheRead) + 1.25*float64(u.CacheWrite)
	return (in*p[0] + float64(u.Output)*p[1]) / 1e6, true
}

// fallbackModels can hand a declined request to another model in the same
// call (server-side fallback, "default" routing); Haiku has none.
var fallbackModels = map[string]bool{
	"claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true, "claude-fable-5-2": true, "claude-fable-5-1": true,
}

// FallbackOptions are the request options for a server-side fallback on
// model, none where it has none.
func FallbackOptions(model string) []option.RequestOption {
	if !fallbackModels[model] {
		return nil
	}
	return []option.RequestOption{
		option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
		option.WithJSONSet("fallbacks", "default"),
	}
}

func toolParams(tools []Tool) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var s struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(t.Schema, &s); err != nil {
			return nil, fmt.Errorf("tool %s: schema: %w", t.Name, err)
		}
		if s.Properties == nil {
			s.Properties = map[string]any{}
		}
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties:  s.Properties,
				Required:    s.Required,
				ExtraFields: map[string]any{"additionalProperties": false},
			},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out, nil
}

// AskAPI answers job on the Anthropic API, sending emit the text as it
// comes and each tool call as it starts. emit runs on the caller's
// goroutine and must not block for long.
func AskAPI(ctx context.Context, job Job, emit func(Event)) (*Answer, error) {
	tools, err := toolParams(job.Tools)
	if err != nil {
		return nil, err
	}
	byName := map[string]Tool{}
	for _, t := range job.Tools {
		byName[t.Name] = t
	}
	maxTurns := job.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12
	}
	opts := []option.RequestOption{option.WithAPIKey(job.APIKey)}
	if job.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(job.BaseURL))
	}
	client := anthropic.NewClient(opts...)

	question, err := json.Marshal(anthropic.NewUserMessage(anthropic.NewTextBlock(job.Question)))
	if err != nil {
		return nil, err
	}
	ans := &Answer{Model: job.Model, Turns: []json.RawMessage{question}}
	messages := make([]anthropic.MessageParam, 0, len(job.History)+1)
	for _, raw := range append(append([]json.RawMessage{}, job.History...), question) {
		messages = append(messages, param.Override[anthropic.MessageParam](raw))
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(job.Model),
		MaxTokens: 32000,
		// The instructions and the tools never change: cached. The
		// conversation is cached up to its last block (top-level).
		System:       []anthropic.TextBlockParam{{Text: job.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(job.Effort)},
		Tools:        tools,
	}
	reqOpts := FallbackOptions(job.Model)

	var text strings.Builder
	for turn := 0; turn < maxTurns; turn++ {
		params.Messages = messages
		stream := client.Messages.NewStreaming(ctx, params, reqOpts...)
		msg := anthropic.Message{}
		for stream.Next() {
			ev := stream.Current()
			if err := msg.Accumulate(ev); err != nil {
				stream.Close()
				return nil, fmt.Errorf("claude: %w", err)
			}
			if d, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if td, ok := d.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
					text.WriteString(td.Text)
					emit(Event{Kind: "text", Text: td.Text})
				}
			}
		}
		if err := stream.Err(); err != nil {
			return nil, apiError(err)
		}
		stream.Close()
		ans.Usage.Input += msg.Usage.InputTokens
		ans.Usage.Output += msg.Usage.OutputTokens
		ans.Usage.CacheRead += msg.Usage.CacheReadInputTokens
		ans.Usage.CacheWrite += msg.Usage.CacheCreationInputTokens
		if msg.Model != "" {
			ans.Model = string(msg.Model)
		}
		ans.Cost, ans.Known = Cost(ans.Model, ans.Usage)

		reply := msg.ToParam()
		raw, err := json.Marshal(reply)
		if err != nil {
			return nil, err
		}
		ans.Turns = append(ans.Turns, raw)
		messages = append(messages, reply)

		switch msg.StopReason {
		case anthropic.StopReasonRefusal:
			return ans, fmt.Errorf("%w (%s)", ErrRefused, msg.StopDetails.Category)
		case anthropic.StopReasonMaxTokens:
			return ans, ErrTooLong
		case anthropic.StopReasonToolUse:
		default:
			ans.Text = strings.TrimSpace(text.String())
			return ans, nil
		}

		// Run the tools, all results in one user turn.
		var results []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			use, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}
			args := json.RawMessage(use.JSON.Input.Raw())
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			ans.Tools = append(ans.Tools, ToolCall{Name: use.Name, Args: args})
			emit(Event{Kind: "tool", Tool: use.Name, Args: args})
			t, found := byName[use.Name]
			if !found {
				results = append(results, anthropic.NewToolResultBlock(use.ID, "unknown tool "+use.Name, true))
				continue
			}
			out, isErr := t.Run(ctx, args)
			results = append(results, anthropic.NewToolResultBlock(use.ID, out, isErr))
		}
		toolTurn := anthropic.NewUserMessage(results...)
		raw, err = json.Marshal(toolTurn)
		if err != nil {
			return nil, err
		}
		ans.Turns = append(ans.Turns, raw)
		messages = append(messages, toolTurn)
		// Text written before a tool call belongs to the answer too; keep a
		// break between it and what follows.
		if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
			text.WriteString("\n\n")
			emit(Event{Kind: "text", Text: "\n\n"})
		}
	}
	return ans, ErrTooLong
}

// ErrBilling is the API refusing for money reasons (credit used up or the
// spend limit reached).
var ErrBilling = errors.New("claude: credit used up or spend limit reached")

// IsBilling tells a refusal for money reasons (e.g. "Your credit balance is
// too low to access the Anthropic API") from other errors. Rate limits
// (429) are not billing problems.
func IsBilling(status int, msg string) bool {
	if status != 400 && status != 402 && status != 403 {
		return false
	}
	msg = strings.ToLower(msg)
	for _, w := range []string{"credit balance", "billing", "spend limit", "spending limit", "purchase credits"} {
		if strings.Contains(msg, w) {
			return true
		}
	}
	return false
}

func apiError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		if IsBilling(apiErr.StatusCode, err.Error()) {
			return fmt.Errorf("%w (HTTP %d)", ErrBilling, apiErr.StatusCode)
		}
		return fmt.Errorf("claude: HTTP %d: %w", apiErr.StatusCode, err)
	}
	return fmt.Errorf("claude: %w", err)
}
