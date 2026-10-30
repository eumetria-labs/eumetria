package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/imgmeta"
)

// Every route of the server and who may use it. routes() is the only place
// a route is registered and each one names its access; a test fails when
// one doesn't (deny by default), and every route but the open ones answers
// 401 without a session. Data routes also need a scope of the user's role
// (internal/auth, roles.go): without it they answer 403.
//
//	GET    /                  page files, compiled in (site.Files) or from -site    open
//	GET    /healthz           200 while syncs succeed, 503 when the data is stale  open
//	POST   /api/login         {name, password, remember} → session cookie, or a    open
//	                          ticket for the second factor (mfa.go)
//	POST   /api/login/code    {ticket, kind: totp|recovery, code} → session        open
//	POST   /api/login/key/begin, …/finish     a key as the second factor           open
//	POST   /api/login/passkey/begin, …/finish  a passkey alone                     open
//	GET    /api/stats         the data the page shows, built for the role's scopes  signed in
//	GET    /days/{name}       older days, one file per year (listed in the stats)  signed in
//	GET    /photos/{name}     a meal or recipe photo, without metadata             signed in, view.photos
//	GET    /routes/{name}     a workout's GPS track                                signed in, view.routes
//	GET    /api/me            who is signed in, with their scopes                  signed in
//	POST   /api/logout        ends this session                                    session
//	POST   /api/logout-all    ends every session of this user                      session
//	GET    /api/sessions      this user's sessions                                 session
//	DELETE /api/sessions/{id} ends one of them                                     session
//	GET    /api/security      this user's second factors                           session
//	POST   /api/security/password  {current, new}: changes the password            session
//	POST   /api/security/key/begin, …/finish   adds a passkey or security key       session, recent sign-in
//	DELETE /api/security/key/{id}              removes one                          session, recent sign-in
//	POST   /api/security/totp/begin, …/confirm  sets up the authenticator app       session, recent sign-in
//	DELETE /api/security/totp                  removes it                           session, recent sign-in
//	POST   /api/security/recovery              ten new recovery codes               session, recent sign-in
//	GET    /api/audit         the audit log                                        admin
//	GET    /api/admin/users   every account (manage.go)                            admin
//	POST   /api/admin/users   {name, password, role}: adds one                     admin, recent sign-in
//	PATCH  /api/admin/users/{id}  {role, disabled, require_second_factor}          admin, recent sign-in
//	POST   /api/admin/users/{id}/password  {password}: a new one, ends sessions    admin, recent sign-in
//	GET    /api/admin/roles   the roles and every scope                            admin
//	PUT    /api/admin/roles/{name}  {scopes}: makes or changes a role              admin, recent sign-in
//	DELETE /api/admin/roles/{name}  deletes one nobody has                         admin, recent sign-in
//	GET    /api/admin/sessions  everyone's sessions                                admin
//	DELETE /api/admin/sessions/{id}  ends one                                      admin
//	GET    /api/status        sync state                                           sync
//	POST   /api/sync          starts a sync now (?all=1 re-fetches everything)     sync
//
// "Signed in" is a session or an API token with the read scope; "admin" a
// session of a role with the admin scope; "sync" a session of a role with
// the sync scope, or such a user's token with the sync scope. Admin and
// sync sessions count only once the user has a second factor. API tokens
// are sent as "Authorization: Bearer eum_…" and made with -create-token;
// with "read" they see what the user's role sees.

type access int

const (
	open       access = iota + 1 // anyone
	signedIn                     // a session, or a token with "read"
	ownSession                   // a session (an API token can't manage sessions)
	adminOnly                    // a session with the admin scope
	syncAccess                   // a session with the sync scope, or a token with "sync"
)

type route struct {
	method, path string
	access       access
	handle       func(http.ResponseWriter, *http.Request, *principal)
}

// principal is who makes a request: a user with a session or an API token.
type principal struct {
	user    *auth.User
	session *auth.Session
	token   *auth.APIToken
}

// can reports whether p may use scope: a session has its role's scopes, a
// token those of them its own scopes cover ("read" the view scopes, "sync"
// syncing).
func (p *principal) can(scope string) bool {
	if !p.user.Can(scope) {
		return false
	}
	switch {
	case p.session != nil:
		return true
	case strings.HasPrefix(scope, "view."):
		return p.token.Has("read")
	case scope == auth.ScopeSync:
		return p.token.Has("sync")
	}
	return false
}

// views are the view scopes p has: what the data is built for.
func (p *principal) views() []string {
	var out []string
	for _, sc := range auth.ViewScopes {
		if p.can(sc) {
			out = append(out, sc)
		}
	}
	return out
}

const sessionCookie = "__Host-eumetria_session"

type app struct {
	runner   *runner
	store    *auth.Store
	data     *published
	photoDir string
	site     http.Handler
	tags     map[string]string
	// Failed sign-ins per account name and per address. An account locks
	// after 5 within 15 minutes, for 15 minutes, doubling up to a day; an
	// address after 20, so one person can't lock everyone out by guessing
	// many names.
	byName, byIP *auth.Limiter
	ready        atomic.Bool  // an account exists
	pending      pendingStore // sign-in tickets and WebAuthn ceremonies
	// One WebAuthn configuration per origin the site is reached at.
	relyingParties sync.Map
}

func newHandler(r *runner, pages fs.FS, data *published, photoDir string, store *auth.Store) http.Handler {
	a := &app{
		runner:   r,
		store:    store,
		data:     data,
		photoDir: photoDir,
		site:     http.FileServerFS(noDirs{pages}),
		tags:     etags(pages),
		byName:   auth.NewLimiter(5, 15*time.Minute, 15*time.Minute, 24*time.Hour),
		byIP:     auth.NewLimiter(20, 15*time.Minute, 15*time.Minute, 24*time.Hour),
	}
	mux := http.NewServeMux()
	for _, rt := range a.routes() {
		mux.Handle(rt.method+" "+rt.path, a.guard(rt))
	}
	return securityHeaders(mux)
}

func (a *app) routes() []route {
	return []route{
		{"GET", "/", open, a.page},
		{"GET", "/healthz", open, a.health},
		{"POST", "/api/login", open, a.login},
		{"POST", "/api/login/code", open, a.loginCode},
		{"POST", "/api/login/key/begin", open, a.loginKeyBegin},
		{"POST", "/api/login/key/finish", open, a.loginKeyFinish},
		{"POST", "/api/login/passkey/begin", open, a.loginPasskeyBegin},
		{"POST", "/api/login/passkey/finish", open, a.loginPasskeyFinish},
		{"GET", "/api/stats", signedIn, a.stats},
		{"GET", "/days/{name}", signedIn, a.file("days")},
		{"GET", "/photos/{name}", signedIn, scoped(auth.ViewPhotos, a.photo)},
		{"GET", "/routes/{name}", signedIn, scoped(auth.ViewRoutes, a.file("routes"))},
		{"GET", "/api/me", signedIn, a.me},
		{"POST", "/api/logout", ownSession, a.logout},
		{"POST", "/api/logout-all", ownSession, a.logoutAll},
		{"GET", "/api/sessions", ownSession, a.sessions},
		{"DELETE", "/api/sessions/{id}", ownSession, a.deleteSession},
		{"GET", "/api/security", ownSession, a.security},
		{"POST", "/api/security/password", ownSession, a.changePassword},
		{"POST", "/api/security/key/begin", ownSession, a.keyBegin},
		{"POST", "/api/security/key/finish", ownSession, a.keyFinish},
		{"DELETE", "/api/security/key/{id}", ownSession, a.keyDelete},
		{"POST", "/api/security/totp/begin", ownSession, a.totpBegin},
		{"POST", "/api/security/totp/confirm", ownSession, a.totpConfirm},
		{"DELETE", "/api/security/totp", ownSession, a.totpDelete},
		{"POST", "/api/security/recovery", ownSession, a.recoveryCodes},
		{"GET", "/api/audit", adminOnly, a.audit},
		{"GET", "/api/admin/users", adminOnly, a.adminUsers},
		{"POST", "/api/admin/users", adminOnly, a.adminCreateUser},
		{"PATCH", "/api/admin/users/{id}", adminOnly, a.adminChangeUser},
		{"POST", "/api/admin/users/{id}/password", adminOnly, a.adminResetPassword},
		{"GET", "/api/admin/roles", adminOnly, a.adminRoles},
		{"PUT", "/api/admin/roles/{name}", adminOnly, a.adminSaveRole},
		{"DELETE", "/api/admin/roles/{name}", adminOnly, a.adminDeleteRole},
		{"GET", "/api/admin/sessions", adminOnly, a.adminSessions},
		{"DELETE", "/api/admin/sessions/{id}", adminOnly, a.adminEndSession},
		{"GET", "/api/status", syncAccess, a.status},
		{"POST", "/api/sync", syncAccess, a.sync},
	}
}

// guard lets a request through to its route only with the access the
// route needs, and only same-origin JSON for anything that changes state.
func (a *app) guard(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		unsafe := req.Method != http.MethodGet && req.Method != http.MethodHead
		bearer := strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ")
		// Cookies go along with requests other sites trigger; tokens don't.
		if unsafe && !bearer && !sameOriginJSON(req) {
			writeError(w, http.StatusForbidden, "cross_site")
			return
		}
		if rt.access == open {
			rt.handle(w, req, nil)
			return
		}
		if !a.hasAccounts(req.Context()) {
			writeError(w, http.StatusServiceUnavailable, "setup")
			return
		}
		p, err := a.principal(req, bearer)
		if err != nil {
			log.Printf("auth: %v", err)
			writeError(w, http.StatusInternalServerError, "server")
			return
		}
		if p == nil {
			writeError(w, http.StatusUnauthorized, "signin")
			return
		}
		if p.session != nil && p.user.Elevated() && (rt.access == adminOnly || rt.access == syncAccess) {
			// Admin and sync rights need the second factor; reading doesn't.
			f, err := a.store.Factors(req.Context(), p.user)
			if err != nil || !f.Any() {
				writeError(w, http.StatusForbidden, "second_factor_required")
				return
			}
		}
		var ok bool
		switch rt.access {
		case signedIn:
			ok = p.session != nil || p.token.Has("read")
		case ownSession:
			ok = p.session != nil
		case adminOnly:
			ok = p.session != nil && p.user.Can(auth.ScopeAdmin)
		case syncAccess:
			ok = p.can(auth.ScopeSync)
		}
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		rt.handle(w, req, p)
	})
}

// scoped lets only principals with scope through to h.
func scoped(scope string, h func(http.ResponseWriter, *http.Request, *principal)) func(http.ResponseWriter, *http.Request, *principal) {
	return func(w http.ResponseWriter, req *http.Request, p *principal) {
		if !p.can(scope) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		h(w, req, p)
	}
}

// sameOriginJSON reports whether a request comes from this site's own page:
// the browser says so (Sec-Fetch-Site), or at least sends a matching
// Origin, and the body is JSON, which a plain HTML form can't send.
func sameOriginJSON(req *http.Request) bool {
	if ct, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type")); ct != "application/json" {
		return false
	}
	switch req.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		o, err := url.Parse(req.Header.Get("Origin"))
		return err == nil && o.Host != "" && o.Host == req.Host
	}
	return false
}

// principal finds who makes the request: an API token in the
// Authorization header, or else a session cookie. nil means nobody.
func (a *app) principal(req *http.Request, bearer bool) (*principal, error) {
	ctx := req.Context()
	if bearer {
		tok, u, err := a.store.APIToken(ctx, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
		if errors.Is(err, auth.ErrNotFound) {
			log.Printf("api: rejected token from %s", clientIP(req))
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &principal{user: u, token: tok}, nil
	}
	c, err := req.Cookie(sessionCookie)
	if err != nil {
		return nil, nil
	}
	sess, u, err := a.store.Session(ctx, c.Value)
	if errors.Is(err, auth.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &principal{user: u, session: sess}, nil
}

// hasAccounts reports whether an account exists. Until one does the server
// shows the page but no data, and says how to make one.
func (a *app) hasAccounts(ctx context.Context) bool {
	if a.ready.Load() {
		return true
	}
	has, err := a.store.HasUsers(ctx)
	if err != nil {
		log.Printf("auth: %v", err)
		return false
	}
	if has {
		a.ready.Store(true)
	}
	return has
}

/* ---------- open ---------- */

func (a *app) page(w http.ResponseWriter, req *http.Request, _ *principal) {
	// With -site the page folder may hold what -out wrote for working on
	// plain files: all of the data, which only comes from /api/stats here.
	if path.Clean(req.URL.Path) == "/stats.json" {
		http.NotFound(w, req)
		return
	}
	if strings.HasPrefix(req.URL.Path, "/fonts/") || strings.HasPrefix(req.URL.Path, "/icons/") {
		w.Header().Set("Cache-Control", "public, max-age=604800")
	} else {
		// Page code: revalidate every time, so a new image shows up at once.
		w.Header().Set("Cache-Control", "no-cache")
	}
	if tag, ok := a.tags[strings.TrimPrefix(path.Clean(req.URL.Path), "/")]; ok {
		w.Header().Set("ETag", tag)
	}
	a.site.ServeHTTP(w, req)
}

func (a *app) health(w http.ResponseWriter, _ *http.Request, _ *principal) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.runner.healthy(time.Now()) {
		http.Error(w, "stale", http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

type loginRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

func (a *app) login(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	if !a.hasAccounts(ctx) {
		writeError(w, http.StatusServiceUnavailable, "setup")
		return
	}
	var in loginRequest
	if !readJSON(w, req, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	ip := clientIP(req)
	nameKey := "name:" + strings.ToLower(in.Name)
	if d := max(a.byIP.Blocked("ip:"+ip), a.byName.Blocked(nameKey)); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "locked", "retry_after": int(d.Seconds()) + 1})
		return
	}
	u, err := a.store.Authenticate(ctx, in.Name, in.Password)
	if errors.Is(err, auth.ErrNotFound) {
		a.byIP.Fail("ip:" + ip)
		a.byName.Fail(nameKey)
		log.Printf("login: failed for %q from %s", truncate(in.Name, 64), ip)
		a.audit_(ctx, nil, truncate(in.Name, 64), "login_failed", "", ip)
		// The same answer whether the name exists or not.
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	if err != nil {
		log.Printf("login: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	f, err := a.store.Factors(ctx, u)
	if err != nil {
		log.Printf("login: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if f.Any() {
		// The password was right; the second factor turns the ticket into
		// a session (mfa.go).
		ticket := a.pending.put("", &pending{user: u, remember: in.Remember})
		writeJSON(w, http.StatusOK, map[string]any{"next": "second_factor", "ticket": ticket, "methods": methods(f)})
		return
	}
	a.signIn(w, req, u, in.Remember, "password")
}

// setSessionCookie hands the browser its session token. The __Host- prefix
// makes browsers refuse it unless it is Secure, for this host only and for
// the whole site; HttpOnly keeps it from scripts; SameSite=Lax keeps it off
// requests other sites trigger, except plain links. Without "Remember me"
// it has no expiry, so the browser drops it when it closes.
func setSessionCookie(w http.ResponseWriter, token string, remember bool) {
	c := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if remember {
		c.MaxAge = int(auth.SessionMax.Seconds())
	}
	http.SetCookie(w, c)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

/* ---------- signed in ---------- */

type me struct {
	Name string `json:"name"`
	Role string `json:"role"`
	// Scopes are every scope the role gives (admin spelled out).
	Scopes       []string `json:"scopes"`
	Sync         bool     `json:"sync"` // may start a sync: the page shows the button
	SecondFactor bool     `json:"second_factor"`
	// An admin, a role that may sync or a user an admin asked, without a
	// second factor: the page asks to set one up.
	NeedsSecondFactor bool `json:"needs_second_factor"`
}

func (a *app) writeMe(w http.ResponseWriter, ctx context.Context, u *auth.User) {
	f, err := a.store.Factors(ctx, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	scopes := []string{}
	for _, sc := range auth.AllScopes {
		if u.Can(sc) {
			scopes = append(scopes, sc)
		}
	}
	writeJSON(w, http.StatusOK, me{Name: u.Name, Role: string(u.Role), Scopes: scopes,
		Sync: u.Can(auth.ScopeSync) && f.Any(), SecondFactor: f.Any(), NeedsSecondFactor: u.NeedsSecondFactor() && !f.Any()})
}

func (a *app) me(w http.ResponseWriter, req *http.Request, p *principal) {
	a.writeMe(w, req.Context(), p.user)
}

func (a *app) logout(w http.ResponseWriter, req *http.Request, p *principal) {
	if err := a.store.DeleteSession(req.Context(), p.user, p.session.ID); err != nil && !errors.Is(err, auth.ErrNotFound) {
		log.Printf("logout: %v", err)
	}
	clearSessionCookie(w)
	a.audit_(req.Context(), p.user, "", "logout", "", clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) logoutAll(w http.ResponseWriter, req *http.Request, p *principal) {
	if err := a.store.DeleteSessions(req.Context(), p.user); err != nil {
		log.Printf("logout: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	clearSessionCookie(w)
	a.audit_(req.Context(), p.user, "", "logout_all", "", clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

type sessionInfo struct {
	ID        int64     `json:"id"`
	Current   bool      `json:"current"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	Expires   time.Time `json:"expires"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
}

func (a *app) sessions(w http.ResponseWriter, req *http.Request, p *principal) {
	list, err := a.store.Sessions(req.Context(), p.user)
	if err != nil {
		log.Printf("sessions: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	out := []sessionInfo{}
	for _, s := range list {
		out = append(out, sessionInfo{s.ID, s.ID == p.session.ID, s.CreatedAt.UTC(), s.LastSeen.UTC(), s.Expires.UTC(), s.IP, s.UserAgent})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) deleteSession(w http.ResponseWriter, req *http.Request, p *principal) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err == nil {
		err = a.store.DeleteSession(req.Context(), p.user, id)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.audit_(req.Context(), p.user, "", "session_ended", strconv.FormatInt(id, 10), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) audit(w http.ResponseWriter, req *http.Request, _ *principal) {
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	list, err := a.store.AuditLog(req.Context(), limit)
	if err != nil {
		log.Printf("audit: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if list == nil {
		list = []auth.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, list)
}

/* ---------- data ---------- */

// stats answers with the data built for the principal's scopes. The page
// revalidates it on every load (ETag), and no shared cache may keep it.
func (a *app) stats(w http.ResponseWriter, req *http.Request, p *principal) {
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Content-Type", "application/json")
	h.Set("ETag", b.etag)
	http.ServeContent(w, req, "", time.Time{}, bytes.NewReader(b.stats))
}

// build returns the principal's stats, answering itself when that fails:
// 403 for a user an admin asked to set up a second factor who has none yet.
func (a *app) build(w http.ResponseWriter, req *http.Request, p *principal) (*build, bool) {
	if p.user.RequireSecondFactor {
		if f, err := a.store.Factors(req.Context(), p.user); err != nil || !f.Any() {
			writeError(w, http.StatusForbidden, "second_factor_required")
			return nil, false
		}
	}
	b, err := a.data.get(p.views(), p.session != nil && p.user.Can(auth.ScopeAdmin))
	if err != nil {
		log.Printf("stats: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return nil, false
	}
	return b, true
}

// Year files, routes and photos are named after their content: the same
// name never changes. "private" keeps them out of shared caches such as
// nginx's.
func immutable(w http.ResponseWriter, contentType string) {
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType)
}

// file serves a year file or a route of the principal's own stats; others'
// names (built for other scopes) are not found.
func (a *app) file(folder string) func(http.ResponseWriter, *http.Request, *principal) {
	return func(w http.ResponseWriter, req *http.Request, p *principal) {
		b, ok := a.build(w, req, p)
		if !ok {
			return
		}
		data, found := b.files[folder+"/"+req.PathValue("name")]
		if !found {
			http.NotFound(w, req)
			return
		}
		immutable(w, "application/json")
		w.Write(data)
	}
}

// photo serves a photo the principal's stats show, from its one copy in
// the data folder, stripped of its metadata (GPS position, camera, time).
func (a *app) photo(w http.ResponseWriter, req *http.Request, p *principal) {
	b, ok := a.build(w, req, p)
	if !ok {
		return
	}
	name := req.PathValue("name")
	if !b.photos[name] {
		http.NotFound(w, req)
		return
	}
	raw, err := os.ReadFile(filepath.Join(a.photoDir, filepath.Base(name)))
	if err != nil {
		http.NotFound(w, req)
		return
	}
	clean, err := imgmeta.Strip(raw)
	if err != nil {
		log.Printf("photo %s: not serving it: %v", name, err)
		http.NotFound(w, req)
		return
	}
	immutable(w, mime.TypeByExtension(filepath.Ext(name)))
	w.Write(clean)
}

/* ---------- sync ---------- */

func (a *app) status(w http.ResponseWriter, _ *http.Request, _ *principal) {
	writeJSON(w, http.StatusOK, a.runner.status())
}

func (a *app) sync(w http.ResponseWriter, req *http.Request, p *principal) {
	all := req.URL.Query().Get("all") == "1"
	code := http.StatusAccepted
	if !a.runner.start(context.WithoutCancel(req.Context()), all) {
		code = http.StatusConflict
	} else {
		detail := "recent days"
		if all {
			detail = "everything"
		}
		if p.token != nil {
			detail += ", token " + p.token.Name
		}
		a.audit_(req.Context(), p.user, "", "sync", detail, clientIP(req))
	}
	writeJSON(w, code, a.runner.status())
}

/* ---------- helpers ---------- */

// audit_ records an entry; a failure to write it is logged, not fatal.
func (a *app) audit_(ctx context.Context, u *auth.User, actor, action, detail, ip string) {
	if err := a.store.Audit(ctx, u, actor, action, detail, ip); err != nil {
		log.Printf("audit: %v", err)
	}
}

// readJSON decodes a small JSON body into v, answering 400 itself when it
// can't.
func readJSON(w http.ResponseWriter, req *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, code int, reason string) {
	writeJSON(w, code, map[string]string{"error": reason})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// clientIP is the address a request came from. Behind nginx the peer is
// nginx (through Docker), so then the address nginx reports counts; from
// a public peer that header could be made up and is ignored.
func clientIP(req *http.Request) string {
	if fromProxy(req) {
		if real := req.Header.Get("X-Real-IP"); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	return host
}
