package main

// The coach chat (phase 12a): a person asks about their data, Claude looks
// it up with the coach tools (internal/coach, the same as the MCP server's)
// and answers. Only on the Anthropic API for now (Settings → AI → Coach);
// phase 12b adds the Claude Code runner.
//
//	GET    /api/coach                 whether it's on, and the user's threads
//	GET    /api/coach/threads/{id}    one thread's messages
//	DELETE /api/coach/threads/{id}    deletes it
//	POST   /api/coach/ask             {thread, text}: asks; the answer streams
//	                                  back as server-sent events
//
// All of them need a session of a role with use.coach. Threads are their
// user's alone. The answer is written to the end even when the page goes
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
	"eumetria/internal/coach"
)

// coachTimeout is the longest an answer may take.
const coachTimeout = 5 * time.Minute

// coachState is what the coach routes share.
type coachState struct {
	busy sync.Map // user id → struct{}: one question at a time each
}

// coachAPIURL is the Anthropic API's address in tests; empty: the real one.
var coachAPIURL string

// coachConfig is the coach's setting: its backend ("off" or "api") and
// whether that backend can answer (an API key is set).
func (a *app) coachConfig(ctx context.Context) (backend string, ready bool, job ai.Job) {
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		log.Printf("coach: settings: %v", err)
		return "off", false, job
	}
	job = ai.Job{APIKey: v.AnthropicKey, Model: v.CoachModel, Effort: v.CoachEffort, System: coach.Instructions, BaseURL: coachAPIURL}
	return v.Coach, v.Coach == "api" && v.AnthropicKey != "", job
}

func (a *app) coachHome(w http.ResponseWriter, req *http.Request, p *principal) {
	backend, ready, _ := a.coachConfig(req.Context())
	threads, err := a.chats.Threads(req.Context(), p.user.ID)
	if err != nil {
		a.failed(w, "coach", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backend": backend, "ready": ready, "threads": threads})
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

// errorCode is what the page is told about a failed answer.
func coachErrorCode(err error) string {
	switch {
	case errors.Is(err, ai.ErrBilling):
		return "billing"
	case errors.Is(err, ai.ErrRefused):
		return "refused"
	case errors.Is(err, ai.ErrTooLong):
		return "too_long"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "failed"
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
	_, ready, job := a.coachConfig(ctx)
	if !ready {
		writeError(w, http.StatusConflict, "coach_off")
		return
	}
	if _, busy := a.coach.busy.LoadOrStore(p.user.ID, struct{}{}); busy {
		writeError(w, http.StatusConflict, "busy")
		return
	}
	release := func() { a.coach.busy.Delete(p.user.ID) }

	var history []json.RawMessage
	if in.Thread != 0 {
		if history, err = a.chats.History(ctx, p.user.ID, in.Thread); err != nil {
			release()
			if errors.Is(err, coach.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found")
				return
			}
			a.failed(w, "coach", err)
			return
		}
	}
	data, err := a.mcpData(ctx, p)
	if err != nil {
		release()
		a.failed(w, "coach", err)
		return
	}
	thread, err := a.chats.Ask(ctx, p.user.ID, in.Thread, question)
	if err != nil {
		release()
		a.failed(w, "coach", err)
		return
	}
	day, _ := time.Parse(time.DateOnly, data.Today)
	job.History = history
	job.Question = coach.Question(data.Today, day.Weekday().String(), question)
	job.Tools = coach.AITools(data)

	// The answer is written to the end even if the page goes away: it runs
	// on its own context and is stored when done.
	events := make(chan ai.Event, 64)
	type result struct {
		msg coach.Message
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer release()
		jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coachTimeout)
		defer cancel()
		ans, err := ai.AskAPI(jctx, job, func(e ai.Event) {
			select {
			case events <- e:
			default: // the page reads too slowly or is gone: it gets the whole answer at the end
			}
		})
		errText := ""
		if err != nil {
			log.Printf("coach: %v", err)
			errText = coachErrorCode(err)
		}
		msg, serr := a.chats.Answered(context.WithoutCancel(ctx), thread, "api", ans, errText)
		if serr != nil {
			log.Printf("coach: storing the answer: %v", serr)
		}
		done <- result{msg, serr}
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
	send("thread", map[string]int64{"id": thread})
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
