package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Permissions are fine-grained scopes; a role is a named set of them.
const (
	ViewNutrition = "view.nutrition" // calories, macros, meals, goals, weight, water
	ViewFoods     = "view.foods"     // food names and amounts, recipes, Open Food Facts data
	ViewPhotos    = "view.photos"    // meal and recipe photos
	ViewActivity  = "view.activity"  // steps, Yazio activities
	ViewHealth    = "view.health"    // Fitbit workouts, heart rate, sleep, recovery, the tracker
	ViewRoutes    = "view.routes"    // GPS tracks of workouts (needs view.health)
	ViewNotes     = "view.notes"     // workout notes
	ViewReview    = "view.review"    // the weekly review
	EditNotes     = "edit.notes"     // add, change and delete notes
	ScopeSync     = "sync"           // sync now, full refresh
	ScopeAdmin    = "admin"          // users, roles, settings, backups, audit log; implies all others
)

// ViewScopes are the scopes that decide which data a user gets.
var ViewScopes = []string{ViewNutrition, ViewFoods, ViewPhotos, ViewActivity, ViewHealth, ViewRoutes, ViewNotes, ViewReview}

// AllScopes are every scope a role can have.
var AllScopes = append(slices.Clone(ViewScopes), EditNotes, ScopeSync, ScopeAdmin)

// The built-in roles. Their scopes are fixed here; they can't be changed
// or deleted.
const (
	RoleAdmin  Role = "admin"  // everything
	RoleViewer Role = "viewer" // all of the data, nothing else
)

var builtinScopes = map[Role][]string{
	RoleAdmin:  {ScopeAdmin},
	RoleViewer: ViewScopes,
}

var (
	ErrRoleBuiltin = errors.New("built-in roles can't be changed")
	ErrRoleInUse   = errors.New("role still has users")
	ErrBadRoleName = errors.New("role name: 1 to 32 letters, digits, spaces and . _ -, starting with a letter or digit")
)

// RoleInfo is a role with its scopes as stored (admin not expanded).
type RoleInfo struct {
	ID      int64    `json:"id"`
	Name    Role     `json:"name"`
	Builtin bool     `json:"builtin"`
	Scopes  []string `json:"scopes"`
}

// Can reports whether the user has scope; admin implies every scope.
func (u *User) Can(scope string) bool {
	return slices.Contains(u.Scopes, scope) || slices.Contains(u.Scopes, ScopeAdmin)
}

// Elevated reports whether the user may change things beyond their own
// account (admin or sync); that needs a second factor.
func (u *User) Elevated() bool { return u.Can(ScopeAdmin) || u.Can(ScopeSync) }

// NeedsSecondFactor reports whether the user must have a second factor:
// to use admin or sync rights, or because an admin requires it.
func (u *User) NeedsSecondFactor() bool { return u.Elevated() || u.RequireSecondFactor }

// NormalizeScopes checks scopes and returns them sorted without
// duplicates. GPS tracks need the workouts they belong to.
func NormalizeScopes(scopes []string) ([]string, error) {
	var out []string
	for _, sc := range scopes {
		if !slices.Contains(AllScopes, sc) {
			return nil, fmt.Errorf("%w: %q", ErrBadScope, sc)
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	if slices.Contains(out, ViewRoutes) && !slices.Contains(out, ViewHealth) && !slices.Contains(out, ScopeAdmin) {
		return nil, fmt.Errorf("%w: %s needs %s", ErrBadScope, ViewRoutes, ViewHealth)
	}
	slices.Sort(out)
	return out, nil
}

// roleScopes are the scopes of a role row: the code's for a built-in role.
func roleScopes(name string, builtin bool, stored string) []string {
	if builtin {
		return builtinScopes[Role(strings.ToLower(name))]
	}
	return strings.Fields(stored)
}

// roleJoin and roleCols read a user's role along with the user.
const (
	roleJoin = " LEFT JOIN roles r ON r.id = u.role_id"
	roleCols = "coalesce(r.name, ''), coalesce(r.builtin, 0), coalesce(r.scopes, '')"
)

func (u *User) setRole(name string, builtin bool, scopes string) {
	u.Role = Role(name)
	u.Scopes = roleScopes(name, builtin, scopes)
}

func validRoleName(name string) bool {
	if name == "" || len(name) > 32 || strings.TrimSpace(name) != name {
		return false
	}
	for i, r := range name {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alnum && (i == 0 || !strings.ContainsRune(" ._-", r)) {
			return false
		}
	}
	return true
}

// roleID finds a role by name (any case).
func (s *Store) roleID(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name Role) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM roles WHERE name = ?", string(name)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("unknown role %q", name)
	}
	return id, err
}

// Roles lists every role, the built-in ones first.
func (s *Store) Roles(ctx context.Context) ([]RoleInfo, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name, builtin, scopes FROM roles ORDER BY builtin DESC, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleInfo
	for rows.Next() {
		var r RoleInfo
		var name, scopes string
		if err := rows.Scan(&r.ID, &name, &r.Builtin, &scopes); err != nil {
			return nil, err
		}
		r.Name, r.Scopes = Role(name), roleScopes(name, r.Builtin, scopes)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRole creates a role or replaces the scopes of one an admin made.
func (s *Store) SaveRole(ctx context.Context, name Role, scopes []string) error {
	if !validRoleName(string(name)) {
		return ErrBadRoleName
	}
	scopes, err := NormalizeScopes(scopes)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var builtin bool
	err = tx.QueryRowContext(ctx, "SELECT builtin FROM roles WHERE name = ?", string(name)).Scan(&builtin)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, "INSERT INTO roles (name, scopes, created_at) VALUES (?, ?, ?)", string(name), strings.Join(scopes, " "), s.now())
	case err == nil && builtin:
		return ErrRoleBuiltin
	case err == nil:
		_, err = tx.ExecContext(ctx, "UPDATE roles SET scopes = ? WHERE name = ?", strings.Join(scopes, " "), string(name))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteRole removes a role an admin made that nobody has any more.
func (s *Store) DeleteRole(ctx context.Context, name Role) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	var builtin bool
	err = tx.QueryRowContext(ctx, "SELECT id, builtin FROM roles WHERE name = ?", string(name)).Scan(&id, &builtin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if builtin {
		return ErrRoleBuiltin
	}
	var users int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE role_id = ?", id).Scan(&users); err != nil {
		return err
	}
	if users > 0 {
		return ErrRoleInUse
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM roles WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRole gives u another role. Their sessions stay; the next request
// already has the new role's scopes.
func (s *Store) SetRole(ctx context.Context, u *User, role Role) error {
	id, err := s.roleID(ctx, s.DB, role)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE users SET role_id = ? WHERE id = ?", id, u.ID)
	return err
}
