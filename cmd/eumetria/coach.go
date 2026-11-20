package main

// The coach chat (phase 12): a person asks about their data, Claude looks
// it up with the coach tools (internal/coach, the same as the MCP server's)
// and answers. Two backends, picked under Settings → AI → Coach:
//
//   - api: the Anthropic API, the tools called in-process (internal/ai).
//   - claude: Claude Code on the owner's subscription, in the runner
//     container (cmd/claude-runner), the tools through /api/mcp with a
//     token made for the one job. Only the owner may use it (Anthropic's
//     terms: the subscription is for its subscriber); everyone else is
//     answered on the API when a key is set. While the subscription's limit
//     is reached, questions wait ("queued") and a worker answers them after
//     the reset, unless "fall back to the API" is on.
//
//	GET    /api/coach                 whether it's on, and the user's threads
//	GET    /api/coach/threads/{id}    one thread's messages
//	DELETE /api/coach/threads/{id}    deletes it
//	POST   /api/coach/ask             {thread, text}: asks; the answer streams
//	                                  back as server-sent events
//
// All of them need a session of a role with use.coach. Threads are their
// user's alone. An answer is written to the end even when the page goes
// away meanwhile; it is there on the next visit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"eumetria/internal/ai"
	"eumetria/internal/auth"
	"eumetria/internal/coach"
	"eumetria/internal/settings"
)

// coachTimeout is the longest an answer may take.
const coachTimeout = 5 * time.Minute

// coachState is what the coach routes share.
type coachState struct {
	busy sync.Map // user id → struct{}: one question at a time each

	mu      sync.Mutex
	working bool // the queue worker runs
}

// coachAPIURL is the Anthropic API's address in tests; empty: the real one.
var coachAPIURL string

// coachRunner is the runner the coach uses (tests replace it).
var coachRunner = func(v settings.Values) ai.Runner {
	return ai.Runner{URL: v.RunnerURL, Key: v.RunnerKey}
}

// coachWorkerEvery is how often the queue worker looks for due answers.
var coachWorkerEvery = 30 * time.Second

func (a *app) settingsValues(ctx context.Context) (settings.Values, bool) {
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		log.Printf("coach: settings: %v", err)
		return v, false
	}
	return v, true
}

// coachBackend is the backend that answers u: "api", "claude", or "" when
// none can.
func (a *app) coachBackend(ctx context.Context, v settings.Values, u *auth.User) string {
	api := ""
	if v.AnthropicKey != "" {
		api = "api"
	}
	switch v.Coach {
	case "api":
		return api
	case "claude":
		if owner, err := a.store.OwnerID(ctx); err == nil && owner == u.ID && coachRunner(v).Ready() {
			return "claude"
		}
		return api
	}
	return ""
}

func (a *app) coachHome(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	v, _ := a.settingsValues(ctx)
	backend := a.coachBackend(ctx, v, p.user)
	threads, err := a.chats.Threads(ctx, p.user.ID)
	if err != nil {
		a.failed(w, "coach", err)
		return
	}
	out := map[string]any{"backend": v.Coach, "ready": backend != "", "uses": backend, "threads": threads}
	if backend == "claude" {
		if until := ai.LimitUntil(ctx, a.store.DB, a.store.Now()); !until.IsZero() {
			out["limit_until"] = until.UTC()
		}
	}
	a.startCoachWorker()
	writeJSON(w, http.StatusOK, out)
}

func threadID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return 0, false
	}
	return id, true
}

func (a *app) coachThread(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := threadID(w, req)
	if !ok {
		return
	}
	msgs, err := a.chats.Messages(req.Context(), p.user.ID, id)
	if errors.Is(err, coach.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "coach", err)
		return
	}
	_, busy := a.coach.busy.Load(p.user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "busy": busy})
}

func (a *app) coachDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := threadID(w, req)
	if !ok {
		return
	}
	err := a.chats.Delete(req.Context(), p.user.ID, id)
	if errors.Is(err, coach.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "coach", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coachErrorCode is what the page is told about a failed answer.
func coachErrorCode(err error) string {
	switch {
	case errors.Is(err, ai.ErrBilling):
		return "billing"
	case errors.Is(err, ai.ErrRefused):
		return "refused"
	case errors.Is(err, ai.ErrTooLong):
		return "too_long"
	case errors.Is(err, ai.ErrRunnerDown):
		return "runner_down"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "failed"
}

// coachJob is one question to answer.
type coachJob struct {
	user     *auth.User
	thread   int64
	msgID    int64 // the queued answer's row; 0 for a new question
	question string
}

// answer answers job on backend and stores the answer, or queues it when
// Claude Code's limit is reached. A failure is a stored answer with an
// error code; the error is only for failing to store it.
func (a *app) answer(ctx context.Context, v settings.Values, backend string, job coachJob, emit func(ai.Event)) (coach.Message, error) {
	store := context.WithoutCancel(ctx)
	finish := func(backend string, ans *ai.Answer, err error) (coach.Message, error) {
		errText := ""
		if err != nil {
			log.Printf("coach (%s): %v", backend, err)
			errText = coachErrorCode(err)
		}
		if job.msgID != 0 {
			return a.chats.Finish(store, job.thread, job.msgID, backend, ans, errText)
		}
		return a.chats.Answered(store, job.thread, backend, ans, errText)
	}
	queue := func(until time.Time) (coach.Message, error) {
		if job.msgID != 0 {
			if err := a.chats.Requeue(store, job.msgID, until); err != nil {
				return coach.Message{}, err
			}
			u := until.UTC()
			return coach.Message{ID: job.msgID, Role: "assistant", Status: "queued", Backend: "claude", QueuedUntil: &u}, nil
		}
		m, err := a.chats.Queue(store, job.thread, until)
		a.startCoachWorker()
		return m, err
	}

	// The earlier questions of the thread; this one isn't answered yet, so
	// it isn't among them.
	turns, err := a.chats.Turns(store, job.user.ID, job.thread)
	if err != nil {
		return finish(backend, nil, err)
	}
	data, err := a.mcpData(store, &principal{user: job.user})
	if err != nil {
		return finish(backend, nil, err)
	}
	day, _ := time.Parse(time.DateOnly, data.Today)
	question := coach.Question(data.Today, day.Weekday().String(), job.question)

	jctx, cancel := context.WithTimeout(store, coachTimeout)
	defer cancel()
	if backend == "claude" {
		now := a.store.Now()
		limit := ai.LimitUntil(store, a.store.DB, now)
		if limit.IsZero() {
			ans, err := a.askClaude(jctx, v, job.user, turns, question, emit)
			if err == nil || !isLimit(err) {
				return finish("claude", ans, err)
			}
			limit = ai.ResetAt(err, now)
			if serr := ai.SetLimit(store, a.store.DB, limit); serr != nil {
				log.Printf("coach: storing the limit: %v", serr)
			}
			log.Printf("coach: Claude Code's limit is reached until %s", limit.Format(time.RFC3339))
		}
		if !(v.ClaudeFallbackAPI && v.AnthropicKey != "") {
			return queue(limit)
		}
	}
	model, effort := v.CoachAI()
	apiJob := ai.Job{APIKey: v.AnthropicKey, Model: model, Effort: effort, System: coach.Instructions, BaseURL: coachAPIURL,
		History: coach.APIHistory(turns), Question: question, Tools: coach.AITools(data)}
	ans, err := ai.AskAPI(jctx, apiJob, emit)
	return finish("api", ans, err)
}

func isLimit(err error) bool {
	var le *ai.LimitError
	return errors.As(err, &le)
}

// askClaude asks Claude Code, with an "mcp" token of u's for this one job:
// the tools see what u's role sees.
func (a *app) askClaude(ctx context.Context, v settings.Values, u *auth.User, turns []coach.Turn, question string, emit func(ai.Event)) (*ai.Answer, error) {
	token, err := a.store.CreateAPIToken(ctx, u, "Claude Code (coach, one answer)", []string{auth.TokenMCP}, a.store.Now().Add(coachTimeout+time.Minute))
	if err != nil {
		return nil, err
	}
	defer func() {
		ctx := context.WithoutCancel(ctx)
		if t, _, err := a.store.APIToken(ctx, token); err == nil {
			a.store.DeleteAPIToken(ctx, u, t.ID)
		}
	}()
	model, effort := v.CoachAI()
	prompt := question
	if t := coach.Transcript(turns); t != "" {
		prompt = "Earlier in this chat:\n\n" + t + "Now:\n\n" + question
	}
	return coachRunner(v).Ask(ctx, ai.RunRequest{
		System: coach.Instructions, Prompt: prompt, Model: model, Effort: effort,
		MCPURL: v.RunnerMCPURL, MCPToken: token, Timeout: int(coachTimeout / time.Second),
	}, emit)
}

func (a *app) coachAsk(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Thread int64  `json:"thread"`
		Text   string `json:"text"`
	}
	if !readJSONUpTo(w, req, &in, 16<<10) {
		return
	}
	question, err := coach.CleanQuestion(in.Text)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_question")
		return
	}
	ctx := req.Context()
	v, _ := a.settingsValues(ctx)
	backend := a.coachBackend(ctx, v, p.user)
	if backend == "" {
		writeError(w, http.StatusConflict, "coach_off")
		return
	}
	if in.Thread != 0 {
		if _, err := a.chats.Turns(ctx, p.user.ID, in.Thread); errors.Is(err, coach.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
	}
	if _, busy := a.coach.busy.LoadOrStore(p.user.ID, struct{}{}); busy {
		writeError(w, http.StatusConflict, "busy")
		return
	}
	thread, err := a.chats.Ask(ctx, p.user.ID, in.Thread, question)
	if err != nil {
		a.coach.busy.Delete(p.user.ID)
		a.failed(w, "coach", err)
		return
	}

	// The answer is written to the end even if the page goes away: it runs
	// on its own context and is stored when done.
	events := make(chan ai.Event, 64)
	type result struct {
		msg coach.Message
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer a.coach.busy.Delete(p.user.ID)
		msg, err := a.answer(ctx, v, backend, coachJob{user: p.user, thread: thread, question: question}, func(e ai.Event) {
			select {
			case events <- e:
			default: // the page reads too slowly or is gone: it gets the whole answer at the end
			}
		})
		if err != nil {
			log.Printf("coach: storing the answer: %v", err)
		}
		done <- result{msg, err}
	}()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: pass each event on at once
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	send("thread", map[string]any{"id": thread, "backend": backend})
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		select {
		case e := <-events:
			send(e.Kind, e)
		case <-beat.C:
			fmt.Fprint(w, ": still thinking\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case r := <-done:
			for len(events) > 0 {
				e := <-events
				send(e.Kind, e)
			}
			if r.err != nil {
				send("error", map[string]string{"error": "failed"})
				return
			}
			send("done", r.msg)
			return
		case <-ctx.Done():
			return // the page is gone; the answer goes on
		}
	}
}

// startCoachWorker starts the queue worker unless it runs or nothing
// waits: it answers the questions that waited for Claude Code's limit once
// it has reset, and stops when none wait.
func (a *app) startCoachWorker() {
	a.coach.mu.Lock()
	defer a.coach.mu.Unlock()
	if a.coach.working {
		return
	}
	waiting, err := a.chats.Waiting(context.Background())
	if err != nil || !waiting {
		return
	}
	a.coach.working = true
	go a.coachWorker()
}

func (a *app) coachWorker() {
	ctx := context.Background()
	defer func() {
		a.coach.mu.Lock()
		a.coach.working = false
		a.coach.mu.Unlock()
	}()
	if err := a.chats.Recover(ctx); err != nil {
		log.Printf("coach queue: %v", err)
		return
	}
	for {
		if waiting, err := a.chats.Waiting(ctx); err != nil || !waiting {
			return
		}
		now := a.store.Now()
		if ai.LimitUntil(ctx, a.store.DB, now).IsZero() {
			a.runDue(ctx, now)
		}
		time.Sleep(coachWorkerEvery)
	}
}

// runDue answers the queued questions that are due, one after the other.
func (a *app) runDue(ctx context.Context, now time.Time) {
	due, err := a.chats.Due(ctx, now)
	if err != nil {
		log.Printf("coach queue: %v", err)
		return
	}
	for i, d := range due {
		v, _ := a.settingsValues(ctx)
		u, err := a.store.UserByID(ctx, d.UserID)
		backend := ""
		if err == nil && !u.Disabled && u.Can(auth.UseCoach) {
			backend = a.coachBackend(ctx, v, u)
		}
		if backend == "" {
			a.chats.Finish(ctx, d.ThreadID, d.MessageID, "", nil, "coach_off")
			continue
		}
		log.Printf("coach queue: answering a question of %s that waited", u.Name)
		msg, err := a.answer(ctx, v, backend, coachJob{user: u, thread: d.ThreadID, msgID: d.MessageID, question: d.Question}, func(ai.Event) {})
		if err != nil {
			log.Printf("coach queue: %v", err)
		}
		if msg.Status == "queued" {
			// The limit again: the rest waits too.
			for _, rest := range due[i+1:] {
				a.chats.Requeue(ctx, rest.MessageID, *msg.QueuedUntil)
			}
			return
		}
	}
}
