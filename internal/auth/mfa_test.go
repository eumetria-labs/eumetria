package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
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
