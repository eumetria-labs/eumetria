package coach

// The coach chat's threads and messages (migration 0030). A thread is its
// user's alone: every read and write names the user, so nobody reaches
// another person's thread by its id.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"eumetria/internal/ai"
	"eumetria/internal/db"
)

// MaxQuestion is the longest question, in characters.
const MaxQuestion = 2000

// HistoryTurns is how many earlier questions (with their answers) a new
// question sends along; older ones stay on the page but Claude no longer
// sees them.
const HistoryTurns = 10

var (
	ErrNotFound    = errors.New("coach: thread not found")
	ErrBadQuestion = errors.New("coach: a question of 1 to 2000 characters")
)

// Thread is one conversation.
type Thread struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message is a question or an answer as the page shows it.
type Message struct {
	ID        int64         `json:"id"`
	Role      string        `json:"role"`
	Text      string        `json:"text"`
	Tools     []ai.ToolCall `json:"tools,omitempty"`
	Status    string        `json:"status"`
	Error     string        `json:"error,omitempty"`
	Backend   string        `json:"backend,omitempty"`
	Model     string        `json:"model,omitempty"`
	CostUSD   float64       `json:"cost_usd,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	// QueuedUntil is when a queued answer is due (Claude Code's limit).
	QueuedUntil *time.Time `json:"queued_until,omitempty"`
}

// Store keeps the threads.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// CleanQuestion trims a question and checks it.
func CleanQuestion(q string) (string, error) {
	q = strings.TrimSpace(q)
	q = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)
	if q == "" || len([]rune(q)) > MaxQuestion {
		return "", ErrBadQuestion
	}
	return q, nil
}

func title(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if r := []rune(q); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return q
}

// Threads lists userID's threads, the latest first.
func (s *Store) Threads(ctx context.Context, userID int64) ([]Thread, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, title, created_at, updated_at FROM coach_threads
		WHERE user_id = ? ORDER BY updated_at DESC, id DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		var t Thread
		var c, u int64
		if err := rows.Scan(&t.ID, &t.Title, &c, &u); err != nil {
			return nil, err
		}
		t.CreatedAt, t.UpdatedAt = time.Unix(c, 0).UTC(), time.Unix(u, 0).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// Owns reports whether thread id is userID's.
func (s *Store) owns(ctx context.Context, userID, id int64) error {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM coach_threads WHERE id = ? AND user_id = ?", id, userID).Scan(&n)
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// Messages lists a thread's messages in order.
func (s *Store) Messages(ctx context.Context, userID, id int64) ([]Message, error) {
	if err := s.owns(ctx, userID, id); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, role, text, tools, status, error, backend, model, cost_usd, created_at, queued_until
		FROM coach_messages WHERE thread_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var tools string
		var at, until int64
		if err := rows.Scan(&m.ID, &m.Role, &m.Text, &tools, &m.Status, &m.Error, &m.Backend, &m.Model, &m.CostUSD, &at, &until); err != nil {
			return nil, err
		}
		if until > 0 && (m.Status == "queued" || m.Status == "running") {
			u := time.Unix(until, 0).UTC()
			m.QueuedUntil = &u
		}
		json.Unmarshal([]byte(tools), &m.Tools)
		m.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// Turn is one answered question of a thread: the question, the answer's
// text and, for an API answer, the Messages API turns it sent.
type Turn struct {
	Question, Answer string
	API              []json.RawMessage
}

// Turns are the last HistoryTurns answered questions of a thread, oldest
// first. Unanswered (failed, queued) ones are left out.
func (s *Store) Turns(ctx context.Context, userID, id int64) ([]Turn, error) {
	if err := s.owns(ctx, userID, id); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT role, text, api, status FROM coach_messages WHERE thread_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Turn
	question := ""
	for rows.Next() {
		var role, text, api, status string
		if err := rows.Scan(&role, &text, &api, &status); err != nil {
			return nil, err
		}
		if role == "user" {
			question = text
			continue
		}
		if status != "done" || question == "" {
			continue
		}
		t := Turn{Question: question, Answer: text}
		json.Unmarshal([]byte(api), &t.API)
		out = append(out, t)
		question = ""
	}
	if len(out) > HistoryTurns {
		out = out[len(out)-HistoryTurns:]
	}
	return out, rows.Err()
}

// APIHistory is turns as Messages API messages: an API answer's own turns
// as sent, any other as its question and answer text.
func APIHistory(turns []Turn) []json.RawMessage {
	var out []json.RawMessage
	for _, t := range turns {
		if len(t.API) > 0 {
			out = append(out, t.API...)
			continue
		}
		q, _ := json.Marshal(map[string]any{"role": "user", "content": t.Question})
		a, _ := json.Marshal(map[string]any{"role": "assistant", "content": t.Answer})
		out = append(out, q, a)
	}
	return out
}

// Transcript is turns as text, for Claude Code, which takes one prompt.
func Transcript(turns []Turn) string {
	var b strings.Builder
	for _, t := range turns {
		b.WriteString("Person: " + t.Question + "\n\nCoach: " + t.Answer + "\n\n")
	}
	return b.String()
}

// Ask stores a question: in thread id, or in a new thread when id is 0.
// It answers the thread's id.
func (s *Store) Ask(ctx context.Context, userID, id int64, question string) (int64, error) {
	now := s.Now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if id == 0 {
		res, err := tx.ExecContext(ctx, "INSERT INTO coach_threads (user_id, title, created_at, updated_at) VALUES (?, ?, ?, ?)",
			userID, title(question), now, now)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else {
		res, err := tx.ExecContext(ctx, "UPDATE coach_threads SET updated_at = ? WHERE id = ? AND user_id = ?", now, id, userID)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, ErrNotFound
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO coach_messages (thread_id, role, text, created_at) VALUES (?, 'user', ?, ?)",
		id, question, now); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// Answered stores an answer (a failed one with errText) and returns it.
func (s *Store) Answered(ctx context.Context, id int64, backend string, a *ai.Answer, errText string) (Message, error) {
	return s.answer(ctx, id, 0, backend, a, errText)
}

// Queue stores an answer that waits until until (Claude Code's limit).
func (s *Store) Queue(ctx context.Context, id int64, until time.Time) (Message, error) {
	now := s.Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO coach_messages (thread_id, role, status, backend, queued_until, created_at)
		VALUES (?, 'assistant', 'queued', 'claude', ?, ?)`, id, until.Unix(), now.Unix())
	if err != nil {
		return Message{}, err
	}
	mid, _ := res.LastInsertId()
	u := until.UTC()
	return Message{ID: mid, Role: "assistant", Status: "queued", Backend: "claude", CreatedAt: now.UTC(), QueuedUntil: &u}, nil
}

// Pending is a queued answer that is due.
type Pending struct {
	MessageID, ThreadID, UserID int64
	Question                    string
}

// Due lists the queued answers due at now, oldest first, and marks them
// running.
func (s *Store) Due(ctx context.Context, now time.Time) ([]Pending, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id, m.thread_id, t.user_id,
		(SELECT q.text FROM coach_messages q WHERE q.thread_id = m.thread_id AND q.role = 'user' AND q.id < m.id ORDER BY q.id DESC LIMIT 1)
		FROM coach_messages m JOIN coach_threads t ON t.id = m.thread_id
		WHERE m.status = 'queued' AND m.queued_until <= ? ORDER BY m.id`, now.Unix())
	if err != nil {
		return nil, err
	}
	var out []Pending
	for rows.Next() {
		var p Pending
		var q sql.NullString
		if err := rows.Scan(&p.MessageID, &p.ThreadID, &p.UserID, &q); err != nil {
			rows.Close()
			return nil, err
		}
		p.Question = q.String
		out = append(out, p)
	}
	rows.Close()
	for _, p := range out {
		if _, err := s.DB.ExecContext(ctx, "UPDATE coach_messages SET status = 'running' WHERE id = ?", p.MessageID); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}

// Requeue puts a running answer back to wait until until.
func (s *Store) Requeue(ctx context.Context, msgID int64, until time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE coach_messages SET status = 'queued', queued_until = ? WHERE id = ?", until.Unix(), msgID)
	return err
}

// Finish stores the answer of a queued question in its row.
func (s *Store) Finish(ctx context.Context, threadID, msgID int64, backend string, a *ai.Answer, errText string) (Message, error) {
	return s.answer(ctx, threadID, msgID, backend, a, errText)
}

// answer writes an answer: a new row, or row msgID when it was queued.
func (s *Store) answer(ctx context.Context, id, msgID int64, backend string, a *ai.Answer, errText string) (Message, error) {
	if a == nil {
		a = &ai.Answer{}
	}
	tools, _ := json.Marshal(a.Tools)
	if a.Tools == nil {
		tools = []byte("[]")
	}
	api := []byte("[]")
	status := "failed"
	if errText == "" {
		status = "done"
		api, _ = json.Marshal(a.Turns)
	}
	now := s.Now()
	mid := msgID
	if msgID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO coach_messages (thread_id, role, text, tools, api, status, error, backend, model,
			input_tokens, output_tokens, cache_read, cache_write, cost_usd, created_at) VALUES (?, 'assistant', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, a.Text, string(tools), string(api), status, errText, backend, a.Model,
			a.Usage.Input, a.Usage.Output, a.Usage.CacheRead, a.Usage.CacheWrite, a.Cost, now.Unix())
		if err != nil {
			return Message{}, err
		}
		mid, _ = res.LastInsertId()
	} else if _, err := s.DB.ExecContext(ctx, `UPDATE coach_messages SET text = ?, tools = ?, api = ?, status = ?, error = ?, backend = ?, model = ?,
		input_tokens = ?, output_tokens = ?, cache_read = ?, cache_write = ?, cost_usd = ?, queued_until = 0, created_at = ? WHERE id = ?`,
		a.Text, string(tools), string(api), status, errText, backend, a.Model,
		a.Usage.Input, a.Usage.Output, a.Usage.CacheRead, a.Usage.CacheWrite, a.Cost, now.Unix(), msgID); err != nil {
		return Message{}, err
	}
	s.DB.ExecContext(ctx, "UPDATE coach_threads SET updated_at = ? WHERE id = ?", now.Unix(), id)
	return Message{ID: mid, Role: "assistant", Text: a.Text, Tools: a.Tools, Status: status, Error: errText,
		Backend: backend, Model: a.Model, CostUSD: a.Cost, CreatedAt: now.UTC()}, nil
}

// Delete removes userID's thread id with its messages.
func (s *Store) Delete(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM coach_threads WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Spent is what the coach's API answers cost since from, for everyone.
func (s *Store) Spent(ctx context.Context, from time.Time) (float64, error) {
	var v sql.NullFloat64
	err := s.DB.QueryRowContext(ctx, "SELECT sum(cost_usd) FROM coach_messages WHERE role = 'assistant' AND created_at >= ?", from.Unix()).Scan(&v)
	return v.Float64, err
}

// Recover puts answers left running by a restart back in the queue, due now.
func (s *Store) Recover(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE coach_messages SET status = 'queued', queued_until = ? WHERE status = 'running'", s.Now().Unix())
	return err
}

// Waiting reports whether any answer is queued or running.
func (s *Store) Waiting(ctx context.Context) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM coach_messages WHERE status IN ('queued', 'running')").Scan(&n)
	return n > 0, err
}
