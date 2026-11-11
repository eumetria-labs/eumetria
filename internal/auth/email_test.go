package auth

import (
	"testing"
	"time"
)

// A new address waits for its confirmation; only then is it the one mail
// goes to, and the link works once, for a day.
func TestEmailConfirmation(t *testing.T) {
	s, now := newStore(t)
	u, _ := s.CreateUser(ctx, "anna", goodPassword, RoleViewer)
	other, _ := s.CreateUser(ctx, "bob", goodPassword+"!", RoleViewer)
	if err := s.SetEmail(ctx, u, "old@example.org"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Email(ctx, u); st.Email != "old@example.org" || st.Verified {
		t.Fatalf("after SetEmail: %+v", st)
	}
	token, err := s.RequestEmail(ctx, u, "new@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Email(ctx, u); st.Email != "old@example.org" || st.Pending != "new@example.org" {
		t.Errorf("waiting: %+v", st)
	}
	if got, _ := s.ConfirmEmail(ctx, other, token); got != "" {
		t.Error("another user confirmed it")
	}
	if got, _ := s.ConfirmEmail(ctx, u, token); got != "new@example.org" {
		t.Fatalf("confirm = %q", got)
	}
	if v, _ := s.VerifiedEmail(ctx, u); v != "new@example.org" {
		t.Errorf("verified = %q", v)
	}
	if got, _ := s.ConfirmEmail(ctx, u, token); got != "" {
		t.Error("the link worked twice")
	}
	// Saving the profile leaves the address alone.
	s.SaveProfile(ctx, u, Profile{FirstName: "Anna", Email: "evil@example.org"})
	if v, _ := s.VerifiedEmail(ctx, u); v != "new@example.org" {
		t.Errorf("after SaveProfile = %q", v)
	}
	// Expired links don't work.
	token, _ = s.RequestEmail(ctx, u, "later@example.org")
	*now = now.Add(EmailTTL + time.Minute)
	if got, _ := s.ConfirmEmail(ctx, u, token); got != "" {
		t.Error("an expired link worked")
	}
	if st, _ := s.Email(ctx, u); st.Pending != "" {
		t.Errorf("an expired change still waits: %+v", st)
	}
}
