package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
)

// Layout is how a user arranged the dashboard: the cards in their order and
// the ones they hid. The server doesn't know the page's cards; it only keeps
// the list short and the names plain, as the page sends them.
type Layout struct {
	Order  []string `json:"order"`
	Hidden []string `json:"hidden"`
	// RetestMonths: after how many months without a result a lift or a
	// benchmark workout is offered for a retest; 0 none, nil the default (6).
	RetestMonths *int `json:"retest_months,omitempty"`
	// PromptTypes: the workout types (as synced, "CROSSFIT") that count as
	// not logged until a training wraps them (PLAN 14c); nil the default
	// (all but walks), empty none.
	PromptTypes *[]string `json:"prompt_types,omitempty"`
}

const (
	maxLayoutCards = 32
	maxPromptTypes = 64
)

var ErrBadLayout = errors.New("layout: up to 32 card names of lowercase letters, digits and -; retest months 0 to 24; up to 64 workout types of capitals, digits and _")

var (
	cardName    = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	workoutType = regexp.MustCompile(`^[A-Z0-9_]{1,48}$`)
)

// Clean checks l and drops repeated names.
func (l Layout) Clean() (Layout, error) {
	clean := func(names []string) ([]string, error) {
		out := []string{}
		if len(names) > maxLayoutCards {
			return nil, ErrBadLayout
		}
		for _, n := range names {
			if !cardName.MatchString(n) {
				return nil, ErrBadLayout
			}
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
		return out, nil
	}
	order, err := clean(l.Order)
	if err != nil {
		return Layout{}, err
	}
	hidden, err := clean(l.Hidden)
	if err != nil {
		return Layout{}, err
	}
	if m := l.RetestMonths; m != nil && (*m < 0 || *m > 24) {
		return Layout{}, ErrBadLayout
	}
	var types *[]string
	if l.PromptTypes != nil {
		if len(*l.PromptTypes) > maxPromptTypes {
			return Layout{}, ErrBadLayout
		}
		list := []string{}
		for _, t := range *l.PromptTypes {
			if !workoutType.MatchString(t) {
				return Layout{}, ErrBadLayout
			}
			if !slices.Contains(list, t) {
				list = append(list, t)
			}
		}
		types = &list
	}
	return Layout{order, hidden, l.RetestMonths, types}, nil
}

// Layout returns u's layout, empty lists when they never arranged the page.
func (s *Store) Layout(ctx context.Context, u *User) (Layout, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT layout FROM profiles WHERE user_id = ?", u.ID).Scan(&raw)
	l := Layout{Order: []string{}, Hidden: []string{}}
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if json.Unmarshal([]byte(raw), &l) != nil {
		return Layout{Order: []string{}, Hidden: []string{}}, nil
	}
	return l.Clean()
}

// SaveLayout stores l, which comes from Clean.
func (s *Store) SaveLayout(ctx context.Context, u *User, l Layout) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO profiles (user_id, layout) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET layout = excluded.layout`, u.ID, string(raw))
	return err
}
