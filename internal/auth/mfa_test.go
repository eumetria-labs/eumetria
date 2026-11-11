package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"eumetria/internal/seal"
)

// The SHA-1 test vectors of RFC 6238, appendix B, cut to six digits as apps
// show them.
func TestTOTPMatchesTheRFC(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		step, ok := CheckTOTP(secret, want, time.Unix(unix, 0), 0)
		if !ok || step != unix/30 {
			t.Errorf("t=%d: %s not accepted (step %d)", unix, want, step)
		}
	}
}

func TestTOTPWindowAndReplay(t *testing.T) {
	secret, _ := NewTOTPSecret()
	key, _ := b32.DecodeString(secret)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cur := now.Unix() / 30
	for d, want := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		if _, ok := CheckTOTP(secret, totpCode(key, cur+d), now, 0); ok != want {
			t.Errorf("code of step %+d accepted = %v, want %v", d, ok, want)
		}
	}
	// A code used once is spent, and so are the ones before it.
	if _, ok := CheckTOTP(secret, totpCode(key, cur), now, cur); ok {
		t.Error("a used code works again")
	}
	if _, ok := CheckTOTP(secret, totpCode(key, cur-1), now, cur); ok {
		t.Error("an older code works after a newer one was used")
	}
	if _, ok := CheckTOTP(secret, "12345", now, 0); ok {
		t.Error("a 5-digit code was accepted")
	}
	if !strings.HasPrefix(TOTPURI(secret, "patrick"), "otpauth://totp/Eumetria:patrick?") {
		t.Error(TOTPURI(secret, "patrick"))
	}
}

func TestStoredFactors(t *testing.T) {
	s, now := newStore(t)
	u, _ := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin)
	if f, _ := s.Factors(ctx, u); f.Any() {
		t.Fatalf("a new user has factors: %+v", f)
	}

	secret, _ := NewTOTPSecret()
	key, _ := b32.DecodeString(secret)
	s.SetTOTP(ctx, u, secret, 0)
	code := totpCode(key, now.Unix()/30)
	if ok, _ := s.UseTOTP(ctx, u, code); !ok {
		t.Fatal("the current code was refused")
	}
	if ok, _ := s.UseTOTP(ctx, u, code); ok {
		t.Error("the same code worked twice")
	}

	codes, err := s.NewRecoveryCodes(ctx, u)
	if err != nil || len(codes) != 10 || len(codes[0]) != 14 {
		t.Fatalf("codes %v, %v", codes, err)
	}
	if f, _ := s.Factors(ctx, u); !f.TOTP || f.RecoveryLeft != 10 || !f.Any() {
		t.Errorf("factors %+v", f)
	}
	// Typed sloppily, it still counts, but only once.
	sloppy := strings.ToUpper(strings.ReplaceAll(codes[3], "-", " "))
	if ok, _ := s.UseRecoveryCode(ctx, u, sloppy); !ok {
		t.Error("a recovery code typed in capitals with spaces was refused")
	}
	if ok, _ := s.UseRecoveryCode(ctx, u, codes[3]); ok {
		t.Error("a recovery code worked twice")
	}
	if f, _ := s.Factors(ctx, u); f.RecoveryLeft != 9 {
		t.Errorf("%d codes left, want 9", f.RecoveryLeft)
	}
	// New codes replace the old ones.
	s.NewRecoveryCodes(ctx, u)
	if ok, _ := s.UseRecoveryCode(ctx, u, codes[5]); ok {
		t.Error("an old recovery code works after new ones were made")
	}

	// Keys, and the user a passkey's handle points to.
	h, _ := s.WebAuthnHandle(ctx, u)
	if h2, _ := s.WebAuthnHandle(ctx, u); string(h) != string(h2) || len(h) != 32 {
		t.Error("the user handle changes or is too short")
	}
	if got, err := s.UserByHandle(ctx, h); err != nil || got.ID != u.ID {
		t.Errorf("UserByHandle: %+v %v", got, err)
	}
	cred := &webauthn.Credential{ID: []byte("cred-1"), PublicKey: []byte("pk")}
	if err := s.AddKey(ctx, u, "YubiKey", cred, false); err != nil {
		t.Fatal(err)
	}
	cred.Authenticator.SignCount = 7
	s.KeyUsed(ctx, u, cred)
	keys, _ := s.Keys(ctx, u)
	if len(keys) != 1 || keys[0].Name != "YubiKey" || keys[0].Credential.Authenticator.SignCount != 7 || keys[0].LastUsed.IsZero() {
		t.Errorf("keys %+v", keys)
	}
	other, _ := s.CreateUser(ctx, "anna", goodPassword+"!", RoleViewer)
	if err := s.DeleteKey(ctx, other, keys[0].ID); err == nil {
		t.Error("another user deleted the key")
	}
	if err := s.DeleteKey(ctx, u, keys[0].ID); err != nil {
		t.Error(err)
	}
	s.DeleteTOTP(ctx, u)
	if f, _ := s.Factors(ctx, u); f.Any() {
		t.Errorf("recovery codes alone count as a second factor: %+v", f)
	}

	// Everything lost: the reset removes all of it and ends the sessions.
	s.SetTOTP(ctx, u, secret, 0)
	s.AddKey(ctx, u, "Phone", &webauthn.Credential{ID: []byte("cred-2")}, true)
	tok, _, _ := s.CreateSession(ctx, u, true, "", "")
	if err := s.ResetFactors(ctx, u); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Factors(ctx, u); f.Any() || f.RecoveryLeft != 0 {
		t.Errorf("after the reset: %+v", f)
	}
	if _, _, err := s.Session(ctx, tok); err == nil {
		t.Error("a session survived the reset")
	}
}

// A new password can end the tokens and passwordless passkeys too; security
// keys used only as a second factor stay.
func TestSetPasswordRevokes(t *testing.T) {
	s, _ := newStore(t)
	u, err := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := s.CreateAPIToken(ctx, u, "t", []string{"read"}, time.Time{})
	s.AddKey(ctx, u, "YubiKey", &webauthn.Credential{ID: []byte("cred-1")}, false)
	s.AddKey(ctx, u, "Phone", &webauthn.Credential{ID: []byte("cred-2")}, true)

	if err := s.SetPassword(ctx, u, "a brand new long password", 0, Revoke{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.APIToken(ctx, tok); err != nil {
		t.Errorf("Revoke{} ended the token: %v", err)
	}
	if err := s.SetPassword(ctx, u, "another brand new password", 0, Revoke{Tokens: true, Passkeys: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.APIToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Errorf("the token survived: %v", err)
	}
	keys, _ := s.Keys(ctx, u)
	if len(keys) != 1 || keys[0].Name != "YubiKey" {
		t.Errorf("keys = %+v, want only the YubiKey", keys)
	}
}

// Each character of a recovery code is as likely as the others.
func TestRandomTextIsEven(t *testing.T) {
	counts := map[rune]int{}
	for range 2000 {
		s, err := randomText(31, recoveryAlphabet)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range s {
			counts[r]++
		}
	}
	if len(counts) != len(recoveryAlphabet) {
		t.Fatalf("%d characters seen, want %d", len(counts), len(recoveryAlphabet))
	}
	// 62000 draws, 2000 per character expected; 5 standard deviations ≈ 220.
	for r, n := range counts {
		if n < 1780 || n > 2220 {
			t.Errorf("%c drawn %d times, want about 2000", r, n)
		}
	}
}

// With SETTINGS_KEY the apps' secrets are sealed in the database, for that
// user only; without the key a sealed one fails closed (pentest F15).
func TestTOTPSecretsAreSealed(t *testing.T) {
	s, now := newStore(t)
	anna, _ := s.CreateUser(ctx, "anna", goodPassword, RoleViewer)
	bob, _ := s.CreateUser(ctx, "bob", goodPassword+"!", RoleViewer)
	secret, _ := NewTOTPSecret()
	// Kept as it is without a key, then sealed once there is one.
	if err := s.SetTOTP(ctx, anna, secret, 0); err != nil {
		t.Fatal(err)
	}
	stored := func(u *User) string {
		var v string
		s.DB.QueryRowContext(ctx, "SELECT secret FROM totp WHERE user_id = ?", u.ID).Scan(&v)
		return v
	}
	if stored(anna) != secret {
		t.Fatal("without a key the secret should be kept as it is")
	}
	box, err := seal.FromHex(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.Box = box
	if n, err := s.SealTOTP(ctx); err != nil || n != 1 {
		t.Fatalf("sealed %d, %v", n, err)
	}
	if v := stored(anna); !strings.HasPrefix(v, "v1:") || strings.Contains(v, secret) {
		t.Errorf("stored = %q", v)
	}
	if n, _ := s.SealTOTP(ctx); n != 0 {
		t.Error("sealed twice")
	}
	code := func() string {
		*now = now.Add(30 * time.Second)
		c, _ := TOTPCode(secret, *now)
		return c
	}
	if ok, err := s.UseTOTP(ctx, anna, code()); !ok || err != nil {
		t.Errorf("a code with the sealed secret: %v %v", ok, err)
	}
	// A new app is sealed right away.
	if err := s.SetTOTP(ctx, bob, secret, 0); err != nil || !strings.HasPrefix(stored(bob), "v1:") {
		t.Errorf("new secret stored as %q, %v", stored(bob), err)
	}
	// Moved to another account, it doesn't open.
	s.DB.ExecContext(ctx, "UPDATE totp SET secret = ? WHERE user_id = ?", stored(anna), bob.ID)
	if _, err := s.UseTOTP(ctx, bob, code()); !errors.Is(err, ErrTOTPSealed) {
		t.Errorf("a secret moved to bob: %v", err)
	}
	// Without the key (or with another one), it fails closed.
	s.Box = nil
	if ok, err := s.UseTOTP(ctx, anna, code()); ok || !errors.Is(err, ErrTOTPSealed) {
		t.Errorf("without the key: %v %v", ok, err)
	}
	other, _ := seal.FromHex(strings.Repeat("cd", 32))
	s.Box = other
	if ok, err := s.UseTOTP(ctx, anna, code()); ok || !errors.Is(err, ErrTOTPSealed) {
		t.Errorf("with another key: %v %v", ok, err)
	}
}
