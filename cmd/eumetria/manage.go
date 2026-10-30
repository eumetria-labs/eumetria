package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"eumetria/internal/auth"
)

// The admin screens (PLAN M5b): users, roles and everyone's sessions. Every
// route here is adminOnly (api.go); what changes something also needs a
// recent sign-in, as changing one's own second factors does. Admins can't
// lock themselves out: not disable or demote their own account, and not
// leave the site without an enabled admin.

// targetUser finds the user named by the path's {id}, answering 404 itself.
func (a *app) targetUser(w http.ResponseWriter, req *http.Request) (*auth.User, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err == nil {
		var u *auth.User
		if u, err = a.store.UserByID(req.Context(), id); err == nil {
			return u, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found")
	return nil, false
}

// failed answers a refused change with the reason the page shows, or 500.
func (a *app) failed(w http.ResponseWriter, what string, err error) {
	var reason string
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, auth.ErrNameTaken):
		reason, code = "name_taken", http.StatusConflict
	case errors.Is(err, auth.ErrBadName):
		reason = "bad_name"
	case errors.Is(err, auth.ErrBadRoleName):
		reason = "bad_role_name"
	case errors.Is(err, auth.ErrBadScope):
		reason = "bad_scopes"
	case errors.Is(err, auth.ErrRoleBuiltin):
		reason, code = "builtin", http.StatusConflict
	case errors.Is(err, auth.ErrRoleInUse):
		reason, code = "in_use", http.StatusConflict
	case errors.Is(err, auth.ErrSelf):
		reason, code = "self", http.StatusConflict
	case errors.Is(err, auth.ErrLastAdmin):
		reason, code = "last_admin", http.StatusConflict
	case errors.Is(err, auth.ErrNotFound), strings.HasPrefix(err.Error(), "unknown role"):
		reason, code = "not_found", http.StatusNotFound
	default:
		if why, ok := passwordReasons[err]; ok {
			reason = why
			break
		}
		log.Printf("%s: %v", what, err)
		reason, code = "server", http.StatusInternalServerError
	}
	writeError(w, code, reason)
}

/* ---------- users ---------- */

func (a *app) adminUsers(w http.ResponseWriter, req *http.Request, _ *principal) {
	list, err := a.store.UserList(req.Context())
	if err != nil {
		a.failed(w, "users", err)
		return
	}
	if list == nil {
		list = []auth.UserInfo{}
	}
	writeJSON(w, http.StatusOK, list)
}

type newUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (a *app) adminCreateUser(w http.ResponseWriter, req *http.Request, p *principal) {
	var in newUser
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	u, err := a.store.CreateUser(req.Context(), strings.TrimSpace(in.Name), in.Password, auth.Role(in.Role))
	if err != nil {
		a.failed(w, "create user", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "user_created", u.Name+" ("+string(u.Role)+")", clientIP(req))
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID})
}

// userChange is what PATCH changes; fields left out stay as they are.
type userChange struct {
	Role                *string `json:"role"`
	Disabled            *bool   `json:"disabled"`
	RequireSecondFactor *bool   `json:"require_second_factor"`
}

func (a *app) adminChangeUser(w http.ResponseWriter, req *http.Request, p *principal) {
	var in userChange
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	u, ok := a.targetUser(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	role, disabled := u.Role, u.Disabled
	if in.Role != nil {
		role = auth.Role(*in.Role)
	}
	if in.Disabled != nil {
		disabled = *in.Disabled
	}
	if (in.Role != nil || in.Disabled != nil) && u.ID == p.user.ID {
		a.failed(w, "change user", auth.ErrSelf)
		return
	}
	left, err := a.store.AdminsLeft(ctx, u, role, disabled)
	if err == nil && left == 0 {
		err = auth.ErrLastAdmin
	}
	if err != nil {
		a.failed(w, "change user", err)
		return
	}
	ip := clientIP(req)
	if in.Role != nil {
		if err := a.store.SetRole(ctx, u, role); err != nil {
			a.failed(w, "change user", err)
			return
		}
		a.audit_(ctx, p.user, "", "role_set", u.Name+" ("+string(role)+")", ip)
	}
	if in.Disabled != nil {
		if err := a.store.SetDisabled(ctx, u, disabled); err != nil {
			a.failed(w, "change user", err)
			return
		}
		action := "user_enabled"
		if disabled {
			action = "user_disabled"
		}
		a.audit_(ctx, p.user, "", action, u.Name, ip)
	}
	if in.RequireSecondFactor != nil {
		if err := a.store.SetRequireSecondFactor(ctx, u, *in.RequireSecondFactor); err != nil {
			a.failed(w, "change user", err)
			return
		}
		a.audit_(ctx, p.user, "", "second_factor_required", u.Name+" ("+strconv.FormatBool(*in.RequireSecondFactor)+")", ip)
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminResetPassword sets a new password for someone who forgot theirs and
// ends their sessions. Admins change their own in the Security dialog.
func (a *app) adminResetPassword(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	u, ok := a.targetUser(w, req)
	if !ok {
		return
	}
	if u.ID == p.user.ID {
		a.failed(w, "reset password", auth.ErrSelf)
		return
	}
	if err := a.store.SetPassword(req.Context(), u, in.Password, 0); err != nil {
		a.failed(w, "reset password", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "password_reset", u.Name, clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

/* ---------- roles ---------- */

func (a *app) adminRoles(w http.ResponseWriter, req *http.Request, _ *principal) {
	roles, err := a.store.Roles(req.Context())
	if err != nil {
		a.failed(w, "roles", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "scopes": auth.AllScopes})
}

func (a *app) adminSaveRole(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Scopes []string `json:"scopes"`
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	ctx := req.Context()
	name := auth.Role(req.PathValue("name"))
	// Taking the admin scope from one's own role would lock one out.
	if strings.EqualFold(string(name), string(p.user.Role)) && p.user.Can(auth.ScopeAdmin) {
		if scopes, err := auth.NormalizeScopes(in.Scopes); err == nil && !contains(scopes, auth.ScopeAdmin) {
			a.failed(w, "save role", auth.ErrSelf)
			return
		}
	}
	if err := a.store.SaveRole(ctx, name, in.Scopes); err != nil {
		a.failed(w, "save role", err)
		return
	}
	a.audit_(ctx, p.user, "", "role_saved", string(name)+" ("+strings.Join(in.Scopes, " ")+")", clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) adminDeleteRole(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	name := auth.Role(req.PathValue("name"))
	if err := a.store.DeleteRole(req.Context(), name); err != nil {
		a.failed(w, "delete role", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "role_deleted", string(name), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

/* ---------- sessions ---------- */

func (a *app) adminSessions(w http.ResponseWriter, req *http.Request, p *principal) {
	list, err := a.store.AllSessions(req.Context())
	if err != nil {
		a.failed(w, "sessions", err)
		return
	}
	type row struct {
		auth.SessionInfo
		Current bool `json:"current"`
	}
	out := []row{}
	for _, s := range list {
		out = append(out, row{s, s.ID == p.session.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) adminEndSession(w http.ResponseWriter, req *http.Request, p *principal) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	name, err := a.store.EndSession(req.Context(), id)
	if err != nil {
		a.failed(w, "end session", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "session_ended", name+" #"+strconv.FormatInt(id, 10), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
