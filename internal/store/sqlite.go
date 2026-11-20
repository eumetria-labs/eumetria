package store

// The history in the app's database (migration 0024_synced.sql). Each
// record keeps its exact JSON in `data`, which is all LoadDB reads; the
// other columns are taken from it on every write, for queries. Mirror.Save
// writes only the records that changed since the last save, which it tells
// by a hash of each record's JSON.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Mirror keeps the history's tables in the database in step with a History.
type Mirror struct {
	DB *sql.DB

	mu   sync.Mutex
	seen map[string][sha256.Size]byte // record key → hash of the JSON in the database
}

// Counts says what a save wrote.
type Counts struct {
	Written, Deleted, Unchanged int
}

func (c Counts) String() string {
	return fmt.Sprintf("%d written, %d deleted, %d unchanged", c.Written, c.Deleted, c.Unchanged)
}

// A record is one row: its kind (the table) and key, and its JSON.
type record struct {
	kind, key string
	data      []byte
	value     any
}

func recordKey(kind, key string) string { return kind + "\x00" + key }

// records lists every record of h, each with its JSON.
func records(h *History) ([]record, error) {
	var out []record
	add := func(kind, key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%s %s: %w", kind, key, err)
		}
		out = append(out, record{kind: kind, key: key, data: b, value: v})
		return nil
	}
	for k, v := range h.Days {
		if err := add("day", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range h.Foods {
		if err := add("food", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range h.Health {
		if err := add("health", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range h.OFF {
		if err := add("off", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range h.Reviews {
		if err := add("review", k, v); err != nil {
			return nil, err
		}
	}
	// The rest is kept only when set, as history.json leaves it out.
	meta := map[string]any{"health_from": h.HealthFrom, "devices": h.Devices, "health_sync": h.HealthSync, "review_state": h.ReviewState}
	if h.HealthFrom == "" {
		delete(meta, "health_from")
	}
	if h.Devices == nil {
		delete(meta, "devices")
	}
	if h.HealthSync == nil {
		delete(meta, "health_sync")
	}
	if h.ReviewState == nil {
		delete(meta, "review_state")
	}
	for k, v := range meta {
		if err := add("meta", k, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// The tables, by record kind: the table and its key column.
var tables = map[string][2]string{
	"day": {"yazio_days", "date"}, "food": {"foods", "key"}, "health": {"health_days", "date"},
	"off": {"off_products", "ean"}, "review": {"weekly_reviews", "week"}, "meta": {"synced_meta", "key"},
}

// savedAtKey is the synced_meta row with the time of the last save.
const savedAtKey = "saved_at"

// SavedAt is when the history was last saved; zero before the first save.
func SavedAt(ctx context.Context, db *sql.DB) (time.Time, error) {
	var data string
	err := db.QueryRowContext(ctx, `SELECT data FROM synced_meta WHERE key = ?`, savedAtKey).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var t time.Time
	return t, json.Unmarshal([]byte(data), &t)
}

// HasSynced reports whether the database holds any synced data yet.
func HasSynced(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM yazio_days) + (SELECT count(*) FROM health_days) + (SELECT count(*) FROM foods)`).Scan(&n)
	return n > 0, err
}

// load learns what the database holds: the hash of each record's JSON.
func (m *Mirror) load(ctx context.Context) error {
	seen := map[string][sha256.Size]byte{}
	for kind, t := range tables {
		rows, err := m.DB.QueryContext(ctx, fmt.Sprintf("SELECT %s, data FROM %s", t[1], t[0]))
		if err != nil {
			return err
		}
		for rows.Next() {
			var key, data string
			if err := rows.Scan(&key, &data); err != nil {
				rows.Close()
				return err
			}
			if kind == "meta" && key == savedAtKey {
				continue // not a record of the history
			}
			seen[recordKey(kind, key)] = sha256.Sum256([]byte(data))
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	m.seen = seen
	return nil
}

// Save writes the records of h that differ from the database and deletes
// the ones h no longer has, in one transaction.
func (m *Mirror) Save(ctx context.Context, h *History) (Counts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var c Counts
	if m.seen == nil {
		if err := m.load(ctx); err != nil {
			return c, fmt.Errorf("reading the synced data: %w", err)
		}
	}
	recs, err := records(h)
	if err != nil {
		return c, err
	}
	next := make(map[string][sha256.Size]byte, len(recs))
	var changed []record
	for _, r := range recs {
		k := recordKey(r.kind, r.key)
		sum := sha256.Sum256(r.data)
		next[k] = sum
		if old, ok := m.seen[k]; ok && old == sum {
			c.Unchanged++
			continue
		}
		changed = append(changed, r)
	}
	var gone []string
	for k := range m.seen {
		if _, ok := next[k]; !ok {
			gone = append(gone, k)
		}
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	// When it was saved, also when nothing changed: the page's "updated".
	if _, err := tx.ExecContext(ctx, `INSERT INTO synced_meta (key, data) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET data = excluded.data`,
		savedAtKey, `"`+time.Now().UTC().Format(time.RFC3339Nano)+`"`); err != nil {
		return c, err
	}
	for _, k := range gone {
		kind, key, _ := strings.Cut(k, "\x00")
		t := tables[kind]
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s = ?", t[0], t[1]), key); err != nil {
			return c, err
		}
		c.Deleted++
	}
	for _, r := range changed {
		if err := write(ctx, tx, r); err != nil {
			return c, fmt.Errorf("%s %s: %w", r.kind, r.key, err)
		}
		c.Written++
	}
	if err := tx.Commit(); err != nil {
		return c, err
	}
	m.seen = next
	return c, nil
}

// write puts one record in its table, with the columns taken from it, and
// rebuilds the index rows of a day.
func write(ctx context.Context, tx *sql.Tx, r record) error {
	exec := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	data := string(r.data)
	switch v := r.value.(type) {
	case *Day:
		var goal, weight any
		if g, ok := v.Goals["energy.energy"]; ok {
			goal = g
		}
		if v.Weight != nil {
			weight = *v.Weight
		}
		if err := exec(`INSERT INTO yazio_days (date, version, fetched_at, energy, protein, carb, fat, energy_goal, steps, activity_energy, water, weight, data)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (date) DO UPDATE SET version = excluded.version, fetched_at = excluded.fetched_at, energy = excluded.energy,
				protein = excluded.protein, carb = excluded.carb, fat = excluded.fat, energy_goal = excluded.energy_goal, steps = excluded.steps,
				activity_energy = excluded.activity_energy, water = excluded.water, weight = excluded.weight, data = excluded.data`,
			r.key, v.Version, timeText(v.FetchedAt), v.Total.Energy, v.Total.Protein, v.Total.Carb, v.Total.Fat, goal, v.Steps,
			v.ActivityEnergy, v.Water, weight, data); err != nil {
			return err
		}
		if err := exec(`DELETE FROM food_entries WHERE date = ?`, r.key); err != nil {
			return err
		}
		for i, it := range v.Items {
			if err := exec(`INSERT INTO food_entries (date, position, entry_id, daytime, name, amount, unit, energy, protein, carb, fat, fiber, sugar, saturated, ai, recipe_id, ean)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.key, i, it.EntryID, it.Daytime, it.Name, it.Amount, it.Unit, it.Energy, it.Protein, it.Carb, it.Fat,
				optional(it.Fiber), optional(it.Sugar), optional(it.Saturated), it.AI, it.RecipeID, it.EAN); err != nil {
				return err
			}
		}
		return nil
	case *Food:
		return exec(`INSERT INTO foods (key, name, unit, missing, data) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET name = excluded.name, unit = excluded.unit, missing = excluded.missing, data = excluded.data`,
			r.key, v.Name, v.Unit, v.Missing, data)
	case *HealthDay:
		var s Sleep
		if v.Sleep != nil {
			s = *v.Sleep
		}
		if err := exec(`INSERT INTO health_days (date, fetched_at, resting_hr, hrv, sleep_start, sleep_end, asleep, awake, deep, light, rem, respiratory, spo2, skin_temp, total_kcal, vo2max, data)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (date) DO UPDATE SET fetched_at = excluded.fetched_at, resting_hr = excluded.resting_hr, hrv = excluded.hrv,
				sleep_start = excluded.sleep_start, sleep_end = excluded.sleep_end, asleep = excluded.asleep, awake = excluded.awake,
				deep = excluded.deep, light = excluded.light, rem = excluded.rem, respiratory = excluded.respiratory, spo2 = excluded.spo2,
				skin_temp = excluded.skin_temp, total_kcal = excluded.total_kcal, vo2max = excluded.vo2max, data = excluded.data`,
			r.key, timeText(v.FetchedAt), v.RestingHR, v.HRV, timeText(s.Start), timeText(s.End), s.Asleep, s.Awake, s.Deep, s.Light, s.REM,
			v.Respiratory, v.SpO2, v.SkinTemp, v.TotalKcal, v.VO2Max, data); err != nil {
			return err
		}
		if err := exec(`DELETE FROM workouts WHERE date = ?`, r.key); err != nil {
			return err
		}
		for _, w := range v.Workouts {
			// A workout that moved to another day is that day's now.
			if err := exec(`INSERT INTO workouts (id, date, type, name, start, end, active_sec, calories, avg_hr, zone_min, distance_m)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET date = excluded.date, type = excluded.type, name = excluded.name, start = excluded.start,
					end = excluded.end, active_sec = excluded.active_sec, calories = excluded.calories, avg_hr = excluded.avg_hr,
					zone_min = excluded.zone_min, distance_m = excluded.distance_m`,
				w.ID, r.key, w.Type, w.Name, timeText(w.Start), timeText(w.End), w.ActiveSec, w.Calories, w.AvgHR, w.ZoneMin, w.DistanceM); err != nil {
				return err
			}
		}
		return nil
	case *OFFProduct:
		return exec(`INSERT INTO off_products (ean, nutriscore, nova, not_found, data) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (ean) DO UPDATE SET nutriscore = excluded.nutriscore, nova = excluded.nova, not_found = excluded.not_found, data = excluded.data`,
			r.key, v.NutriScore, v.NOVA, v.NotFound, data)
	case *Review:
		return exec(`INSERT INTO weekly_reviews (week, model, cost_usd, created_at, data) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (week) DO UPDATE SET model = excluded.model, cost_usd = excluded.cost_usd, created_at = excluded.created_at, data = excluded.data`,
			r.key, v.Model, v.CostUSD, timeText(v.CreatedAt), data)
	default:
		return exec(`INSERT INTO synced_meta (key, data) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET data = excluded.data`, r.key, data)
	}
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func optional(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// LoadDB reads the history from the database: the records' JSON, nothing
// else.
func LoadDB(ctx context.Context, db *sql.DB) (*History, error) {
	h := &History{Days: map[string]*Day{}, Foods: map[string]*Food{}, Health: map[string]*HealthDay{}, OFF: map[string]*OFFProduct{}}
	read := func(table, key string, put func(key string, data []byte) error) error {
		rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT %s, data FROM %s", key, table))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var data []byte
			if err := rows.Scan(&k, &data); err != nil {
				return err
			}
			if err := put(k, data); err != nil {
				return fmt.Errorf("%s %s: %w", table, k, err)
			}
		}
		return rows.Err()
	}
	err := errors.Join(
		read("yazio_days", "date", func(k string, b []byte) error { v := &Day{}; h.Days[k] = v; return json.Unmarshal(b, v) }),
		read("foods", "key", func(k string, b []byte) error { v := &Food{}; h.Foods[k] = v; return json.Unmarshal(b, v) }),
		read("health_days", "date", func(k string, b []byte) error { v := &HealthDay{}; h.Health[k] = v; return json.Unmarshal(b, v) }),
		read("off_products", "ean", func(k string, b []byte) error { v := &OFFProduct{}; h.OFF[k] = v; return json.Unmarshal(b, v) }),
		read("weekly_reviews", "week", func(k string, b []byte) error {
			if h.Reviews == nil {
				h.Reviews = map[string]*Review{}
			}
			v := &Review{}
			h.Reviews[k] = v
			return json.Unmarshal(b, v)
		}),
		read("synced_meta", "key", func(k string, b []byte) error {
			switch k {
			case "health_from":
				return json.Unmarshal(b, &h.HealthFrom)
			case "devices":
				return json.Unmarshal(b, &h.Devices)
			case "health_sync":
				return json.Unmarshal(b, &h.HealthSync)
			case "review_state":
				return json.Unmarshal(b, &h.ReviewState)
			}
			return nil // a key a newer version wrote
		}),
	)
	if err != nil {
		return nil, err
	}
	return h, nil
}
