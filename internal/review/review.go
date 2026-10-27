package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ErrBilling means the API refused for money reasons: the prepaid credit
// is used up or the workspace's spend limit is reached. Refused requests
// cost nothing, so trying again later is safe.
var ErrBilling = errors.New("claude: credit used up or spend limit reached")

// DefaultModel writes the review unless AI_MODEL names another.
const DefaultModel = "claude-opus-5-5"

// The instructions are fixed; only the week's data changes between
// requests.
const instructions = `You are the coach on Eumetria, a private dashboard of one person's own nutrition (logged in Yazio), training and sleep (recorded by a Fitbit), and you write their weekly review. Talk to that person directly ("you"), like a friendly, encouraging coach who knows the numbers: warm and direct, never preachy.

Rules:
- Use only the data given. Cite the numbers you rely on. Where data is missing, say so briefly instead of guessing.
- Totals, averages and shares are precomputed in "this_week" and "previous_week"; use them rather than adding up days yourself.
- No medical advice, no diagnoses, no made-up scores or ratings, no moralizing about food. Nutri-Score and NOVA are labels from Open Food Facts, a crowd-sourced database: not every food has them ("unknown").
- "kcal_goal_incl_activity" already includes the calories of the day's activity.
- Write numbers the German way, as the dashboard does: a dot between thousands and a comma for decimals (2.071 kcal, 78,6 kg, 8.307 steps).
- Durations come formatted ("7 h 18 min", "37 min"); write them the same way, never as a number of minutes (not "438 minutes").
- Sleep belongs to the morning it ended; name nights as in its "night" field (e.g. "the night from Thursday to Friday").
- Weight: compare the weekly averages ("avg_weight_kg"). Single weigh-ins swing with water and food, so don't draw conclusions from the first and last one alone.
- Mention Nutri-Score or NOVA only if "share_of_kcal_with_labels" is at least 0.5; below that, leave food labels out.
- If the week is still running, say which days it covers and treat today's totals as incomplete.

Format (Markdown, no tables, at most 250 words):
- No title or heading of your own; the page shows which week it is.
- Start with one short sentence (at most 20 words) that sums up the week. It is shown on its own as a teaser, so it has to make sense without the rest.
- "Nutrition", "Training" and "Sleep & recovery": two to four short bullet points each, with comparisons to the previous week where they say something.
- "Worth a look": one to three concrete observations from the data (patterns, outliers, changes), phrased as observations, not orders.

Languages: write the review twice, first in English, then the same review in German, with a line that holds only "` + LangSeparator + `" between them. The German version says exactly what the English one says, with the same numbers in the same format and the same structure: informal "du"; the headings "Ernährung", "Training", "Schlaf & Erholung" and "Einen Blick wert"; nights named like "die Nacht von Donnerstag auf Freitag"; again a first sentence of at most 20 words that works on its own. Each version has at most 250 words.`

// LangSeparator is the line between the English and the German review in
// Claude's answer.
const LangSeparator = "=== DEUTSCH ==="

// Result is a written review and what it cost.
type Result struct {
	Text         string // English
	TextDE       string // German; empty when the answer had no German part
	Model        string // the model that answered
	InputTokens  int64
	OutputTokens int64 // includes the model's thinking
	CostUSD      float64
	KnownPrice   bool
}

// prices are USD per million input and output tokens (first-party API).
var prices = map[string][2]float64{
	"claude-opus-5-5":   {4, 20},
	"claude-sonnet-5-5": {2, 10},
	"claude-haiku-5-5":  {0.10, 0.50},
	"claude-fable-5-1":  {10, 50},
}

// fallbackModels can hand a declined request to another model in the same
// call (server-side fallback); Haiku has none.
var fallbackModels = map[string]bool{
	"claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true, "claude-fable-5-1": true,
}

// encode writes w as JSON with "&", "<" and ">" as they are: Go escapes
// them by default (\u0026), and the model then answers with HTML escapes
// ("Sleep &amp; recovery").
func encode(w Week) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(w); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// Generate asks Claude for the review of w. The API key comes from
// ANTHROPIC_API_KEY; effort is "low", "medium", "high", "xhigh" or "max".
func Generate(ctx context.Context, model, effort string, w Week) (*Result, error) {
	data, err := encode(w)
	if err != nil {
		return nil, err
	}
	week := fmt.Sprintf("Calendar week %s (Monday) to %s (Sunday).", w.From, w.To)
	if w.Running {
		week += " The week is still running."
	}
	client := anthropic.NewClient()
	var opts []option.RequestOption
	if fallbackModels[model] {
		opts = append(opts,
			option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
			option.WithJSONSet("fallbacks", "default"))
	}
	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:        anthropic.Model(model),
		MaxTokens:    16000,
		System:       []anthropic.TextBlockParam{{Text: instructions}},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(effort)},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(week + " The data:\n\n```json\n" + data + "\n```")),
		},
	}, opts...)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			if billing(apiErr.StatusCode, err.Error()) {
				return nil, fmt.Errorf("%w (HTTP %d): %v", ErrBilling, apiErr.StatusCode, err)
			}
			return nil, fmt.Errorf("claude: HTTP %d: %w", apiErr.StatusCode, err)
		}
		return nil, fmt.Errorf("claude: %w", err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("claude declined the request (%s): %s", resp.StopDetails.Category, resp.StopDetails.Explanation)
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	if resp.StopReason == anthropic.StopReasonMaxTokens {
		return nil, errors.New("claude: the answer hit the token limit")
	}
	en, de := splitLanguages(text.String())
	r := &Result{
		Text: en, TextDE: de, Model: string(resp.Model),
		InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
	}
	if p, ok := prices[r.Model]; ok {
		r.KnownPrice = true
		r.CostUSD = (float64(r.InputTokens)*p[0] + float64(r.OutputTokens)*p[1]) / 1e6
	}
	return r, nil
}

// The separator line, also when the model dresses it up ("**=== DEUTSCH ===**").
var separator = regexp.MustCompile(`(?m)^[\s*_#>-]*` + regexp.QuoteMeta(LangSeparator) + `[\s*_]*$`)

// splitLanguages separates the English and the German review. Without the
// separator everything counts as English: the review is kept rather than
// paid for again.
func splitLanguages(s string) (en, de string) {
	loc := separator.FindStringIndex(s)
	if loc == nil {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:loc[0]]), strings.TrimSpace(s[loc[1]:])
}

// billing tells a refusal for money reasons (e.g. "Your credit balance is
// too low to access the Anthropic API") from other errors. Rate limits
// (429) are not billing problems.
func billing(status int, msg string) bool {
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
