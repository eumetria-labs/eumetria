package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/db"
)

var ctx = context.Background()

const goodPassword = "three sweaty protein shakes"

func TestPasswordHashes(t *testing.T) {
	h, err := HashPassword(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash %q doesn't carry its parameters", h)
	}
	if !CheckPassword(h, goodPassword) || CheckPassword(h, goodPassword+"!") || CheckPassword(h, "") {
		t.Error("CheckPassword accepts the wrong password or rejects the right one")
	}
	if h2, _ := HashPassword(goodPassword); h2 == h {
		t.Error("two hashes of one password are equal: the salt isn't random")
	}
	for _, broken := range []string{"", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA", h[:len(h)-4] + "!!!!", strings.Replace(h, "v=19", "v=1", 1)} {
		if CheckPassword(broken, goodPassword) {
			t.Errorf("accepted a broken hash %q", broken)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	for pw, want := range map[string]error{
		goodPassword:                         nil,
		"short-pass":                         ErrPasswordShort,
		"ääääääääääää":                       nil, // 12 characters, 24 bytes
		"ääääääääääa":                        ErrPasswordShort,
		"Password1234":                       ErrPasswordCommon, // in the list in lower case
		"MY-OWN-ACCOUNT":                     ErrPasswordIsName,
		strings.Repeat("x", 1025):            ErrPasswordLong,
		"correct horse battery staple again": nil,
	} {
		if got := ValidatePassword(pw, "my-own-account"); !errors.Is(got, want) {
			t.Errorf("%q: got %v, want %v", pw, got, want)
		}
	}
}

func TestLimiterLocksAndDoubles(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(3, 15*time.Minute, 15*time.Minute, time.Hour)
	l.Now = func() time.Time { return now }
	for range 2 {
		l.Fail("user:a")
	}
	if l.Blocked("user:a") != 0 {
		t.Fatal("locked before reaching the limit")
	}
	l.Fail("user:a")
	if d := l.Blocked("user:a"); d != 15*time.Minute {
		t.Fatalf("first lockout %v, want 15m", d)
	}
	if l.Blocked("user:b") != 0 {
		t.Error("another key is locked too")
	}
	// After it ends, three more failures lock for twice as long, then the cap.
	for _, want := range []time.Duration{30 * time.Minute, time.Hour, time.Hour} {
		now = now.Add(l.Blocked("user:a") + time.Second)
		for range 3 {
			l.Fail("user:a")
		}
		if d := l.Blocked("user:a"); d != want {
			t.Errorf("lockout %v, want %v", d, want)
		}
	}
	// A success forgets everything.
	l.Reset("user:a")
	if l.Blocked("user:a") != 0 {
		t.Error("still locked after Reset")
	}
	// Failures spread wider than the window don't add up.
	for range 5 {
		l.Fail("user:c")
		now = now.Add(10 * time.Minute)
		l.Fail("user:c")
		now = now.Add(16 * time.Minute)
	}
	if l.Blocked("user:c") != 0 {
		t.Error("failures outside the window locked the key")
	}
}

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s := NewStore(d)
	s.Now = func() time.Time { return now }
	return s, &now
}

func TestUsers(t *testing.T) {
	s, _ := newStore(t)
	if has, _ := s.HasUsers(ctx); has {
		t.Fatal("a new database has users")
	}
	u, err := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if has, _ := s.HasUsers(ctx); !has {
		t.Error("HasUsers after creating one")
	}
	if _, err := s.CreateUser(ctx, "Patrick", goodPassword, RoleViewer); !errors.Is(err, ErrNameTaken) {
		t.Errorf("same name in other case: %v, want ErrNameTaken", err)
	}
	for _, name := range []string{"", "-x", "a b", "<script>", strings.Repeat("a", 65)} {
		if _, err := s.CreateUser(ctx, name, goodPassword, RoleViewer); !errors.Is(err, ErrBadName) {
			t.Errorf("name %q: %v, want ErrBadName", name, err)
		}
	}
	if _, err := s.CreateUser(ctx, "anna", "password1234", RoleViewer); !errors.Is(err, ErrPasswordCommon) {
		t.Errorf("weak password: %v", err)
	}

	if got, err := s.Authenticate(ctx, "PATRICK", goodPassword); err != nil || got.ID != u.ID || got.Role != RoleAdmin {
		t.Errorf("sign-in with the name in other case: %+v, %v", got, err)
	}
	for name, pw := range map[string]string{"patrick": "wrong password here", "nobody": goodPassword} {
		if _, err := s.Authenticate(ctx, name, pw); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", name, err)
		}
	}
	s.DB.Exec("UPDATE users SET disabled = 1 WHERE id = ?", u.ID)
	if _, err := s.Authenticate(ctx, "patrick", goodPassword); !errors.Is(err, ErrNotFound) {
		t.Errorf("a disabled user signed in: %v", err)
	}
}

func TestSessions(t *testing.T) {
	s, now := newStore(t)
	u, _ := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin)
	other, _ := s.CreateUser(ctx, "anna", goodPassword+"!", RoleViewer)

	long, sess, err := s.CreateSession(ctx, u, true, "203.0.113.5", "Firefox")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(long, sess.UserAgent) || len(long) < 40 {
		t.Fatalf("token %q", long)
	}
	var stored string
	s.DB.QueryRow("SELECT token_hash FROM sessions WHERE id = ?", sess.ID).Scan(&stored)
	if stored == long || stored != HashToken(long) {
		t.Error("the database holds the token itself, not its hash")
	}
	short, _, _ := s.CreateSession(ctx, u, false, "", "")

	if _, got, err := s.Session(ctx, long); err != nil || got.ID != u.ID {
		t.Fatalf("Session: %+v, %v", got, err)
	}
	if _, _, err := s.Session(ctx, long+"x"); !errors.Is(err, ErrNotFound) {
		t.Error("a wrong token found a session")
	}

	// 13 hours later the short one is gone, the remembered one lives on.
	*now = now.Add(13 * time.Hour)
	if _, _, err := s.Session(ctx, short); !errors.Is(err, ErrNotFound) {
		t.Error("a session without Remember me outlived 12 hours")
	}
	if _, _, err := s.Session(ctx, long); err != nil {
		t.Errorf("remembered session: %v", err)
	}
	// Used now and then it lasts; a week unused ends it.
	for range 3 {
		*now = now.Add(6 * 24 * time.Hour)
		if _, _, err := s.Session(ctx, long); err != nil {
			t.Fatalf("session used every 6 days: %v", err)
		}
	}
	*now = now.Add(7*24*time.Hour + time.Minute)
	if _, _, err := s.Session(ctx, long); !errors.Is(err, ErrNotFound) {
		t.Error("a session unused for more than a week lives on")
	}

	// Used daily it still ends after 30 days.
	long, _, _ = s.CreateSession(ctx, u, true, "", "")
	for i := range 29 {
		*now = now.Add(24 * time.Hour)
		if _, _, err := s.Session(ctx, long); err != nil {
			t.Fatalf("day %d: %v", i+1, err)
		}
	}
	*now = now.Add(25 * time.Hour)
	if _, _, err := s.Session(ctx, long); !errors.Is(err, ErrNotFound) {
		t.Error("a session lives longer than 30 days")
	}

	// Another user can't end someone else's session; a password change ends all.
	tok, sess, _ := s.CreateSession(ctx, u, true, "", "")
	if err := s.DeleteSession(ctx, other, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another user deleted the session: %v", err)
	}
	if list, _ := s.Sessions(ctx, u); len(list) != 1 {
		t.Errorf("Sessions: %d, want 1", len(list))
	}
	keep, kept, _ := s.CreateSession(ctx, u, true, "", "")
	if err := s.SetPassword(ctx, u, "a brand new long password", kept.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Session(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Error("a session survived a password change")
	}
	if _, _, err := s.Session(ctx, keep); err != nil {
		t.Errorf("the session that changed the password ended: %v", err)
	}
	if ok, _ := s.PasswordMatches(ctx, u, "a brand new long password"); !ok {
		t.Error("PasswordMatches refuses the new password")
	}
	if ok, _ := s.PasswordMatches(ctx, u, goodPassword); ok {
		t.Error("PasswordMatches takes the old password")
	}
	if _, err := s.Authenticate(ctx, "patrick", "a brand new long password"); err != nil {
		t.Errorf("new password: %v", err)
	}
}

func TestAPITokens(t *testing.T) {
	s, now := newStore(t)
	u, _ := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin)
	if _, err := s.CreateAPIToken(ctx, u, "x", []string{"admin"}, time.Time{}); !errors.Is(err, ErrBadScope) {
		t.Errorf("unknown scope: %v", err)
	}
	tok, err := s.CreateAPIToken(ctx, u, "cron", []string{"sync"}, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, APITokenPrefix) {
		t.Errorf("token %q lacks the prefix", tok)
	}
	got, owner, err := s.APIToken(ctx, tok)
	if err != nil || owner.ID != u.ID || !got.Has("sync") || got.Has("read") {
		t.Fatalf("APIToken: %+v %+v %v", got, owner, err)
	}
	// A session token is no API token, and the other way round.
	sess, _, _ := s.CreateSession(ctx, u, true, "", "")
	if _, _, err := s.APIToken(ctx, sess); !errors.Is(err, ErrNotFound) {
		t.Error("a session token works as an API token")
	}
	if _, _, err := s.Session(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Error("an API token works as a session")
	}
	*now = now.Add(49 * time.Hour)
	if _, _, err := s.APIToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Error("an expired token still works")
	}
}

func TestAuditLog(t *testing.T) {
	s, _ := newStore(t)
	u, _ := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin)
	s.Audit(ctx, nil, "nobody", "login_failed", "", "203.0.113.5")
	s.Audit(ctx, u, "", "login", "", "203.0.113.5")
	log, err := s.AuditLog(ctx, 10)
	if err != nil || len(log) != 2 {
		t.Fatalf("%v, %v", log, err)
	}
	if log[0].Action != "login" || log[0].Actor != "patrick" || log[1].Actor != "nobody" {
		t.Errorf("newest first, actor from the user: %+v", log)
	}
}
