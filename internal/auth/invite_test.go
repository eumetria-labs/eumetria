package auth

import (
	"errors"
	"testing"
	"time"
)

func TestInvites(t *testing.T) {
	s, now := newStore(t)
	u, err := s.CreateInvited(ctx, "anna", RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	old, _, _ := s.NewInvite(ctx, u, "anna@example.org")
	token, _, _ := s.NewInvite(ctx, u, "anna@example.org")
	if _, err := s.Invite(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Error("an older invitation still works")
	}
	if _, err := s.AcceptInvite(ctx, token, "anna"); !errors.Is(err, ErrPasswordShort) {
		t.Errorf("a bad password: %v", err)
	}
	got, err := s.AcceptInvite(ctx, token, goodPassword)
	if err != nil || got.ID != u.ID {
		t.Fatalf("accept: %+v %v", got, err)
	}
	if _, err := s.Authenticate(ctx, "anna", goodPassword); err != nil {
		t.Errorf("the chosen password: %v", err)
	}
	if v, _ := s.VerifiedEmail(ctx, u); v != "anna@example.org" {
		t.Errorf("address = %q, want it confirmed", v)
	}
	if _, err := s.AcceptInvite(ctx, token, goodPassword+"!"); !errors.Is(err, ErrNotFound) {
		t.Error("the invitation worked twice")
	}
	// Expired.
	b, _ := s.CreateInvited(ctx, "bob", RoleViewer)
	token, _, _ = s.NewInvite(ctx, b, "bob@example.org")
	*now = now.Add(InviteTTL + time.Minute)
	if _, err := s.AcceptInvite(ctx, token, goodPassword); !errors.Is(err, ErrNotFound) {
		t.Error("an expired invitation worked")
	}
}
