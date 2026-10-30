package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"eumetria/internal/auth"
	"eumetria/internal/db"
)

// setupAccounts runs when the server starts. Without any account it serves
// no data and says how to make the first one; -demo makes one itself.
func setupAccounts(ctx context.Context, store *auth.Store, demo bool) error {
	has, err := store.HasUsers(ctx)
	if err != nil || has {
		return err
	}
	if demo {
		if _, err := store.CreateDemoUser(ctx, "demo", "demo"); err != nil {
			return err
		}
		log.Printf("demo: sign in as demo / demo")
		return nil
	}
	log.Printf("no account yet: create the first with -create-user <name> -role admin (README, \"Setup on a server\")")
	return nil
}

// snapshotLoop writes a consistent copy of the database to dir/app.db at
// start and every night at 03:20, before the host's backup at 03:30 (which
// leaves the live app.db out: copying it mid-write can give a broken copy).
func snapshotLoop(ctx context.Context, d *db.DB, dir string) {
	for {
		if err := writeSnapshot(ctx, d, dir); err != nil {
			log.Printf("database snapshot: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextAt(time.Now(), 3, 20))):
		}
	}
}

// writeSnapshot replaces dir/app.db only once the new copy is complete.
func writeSnapshot(ctx context.Context, d *db.DB, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "app.db.tmp")
	os.Remove(tmp)
	if err := d.Snapshot(ctx, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "app.db"))
}

// nextAt is the next hh:mm local time after now.
func nextAt(now time.Time, hour, min int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

/* ---------- command line ---------- */

// accountCommand runs one of the account and role commands (-create-user,
// -set-role, -create-role, …) on the database in the data folder, next to
// a running server if need be. Roles take effect with a user's next
// request.
type accountCommand struct {
	createUser, resetPassword, createToken string
	resetFactors                           string
	setRole, createRole, deleteRole        string
	listRoles                              bool
	role                                   string
	tokenName, scopes                      string
	expiresDays                            int
}

func (c accountCommand) any() bool {
	return c.createUser != "" || c.resetPassword != "" || c.createToken != "" || c.resetFactors != "" ||
		c.setRole != "" || c.createRole != "" || c.deleteRole != "" || c.listRoles
}

// scopeList splits a comma- or space-separated -scopes value.
func scopeList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func runAccountCommand(ctx context.Context, cfg config, c accountCommand) error {
	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		return err
	}
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer d.Close()
	store := auth.NewStore(d)
	in := bufio.NewReader(os.Stdin)
	switch {
	case c.createUser != "":
		pw, err := newPassword(in, c.createUser)
		if err != nil {
			return err
		}
		u, err := store.CreateUser(ctx, c.createUser, pw, auth.Role(c.role))
		if err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "user_created", u.Name+" ("+c.role+")", "")
		fmt.Printf("Created %s (%s). Sign in at the site.\n", u.Name, u.Role)
	case c.resetPassword != "":
		u, err := store.UserByName(ctx, c.resetPassword)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.resetPassword, err)
		}
		pw, err := newPassword(in, u.Name)
		if err != nil {
			return err
		}
		if err := store.SetPassword(ctx, u, pw, 0); err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "password_reset", u.Name, "")
		fmt.Printf("New password set for %s; all of their sessions have ended.\n", u.Name)
	case c.resetFactors != "":
		u, err := store.UserByName(ctx, c.resetFactors)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.resetFactors, err)
		}
		if err := store.ResetFactors(ctx, u); err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "second_factors_reset", u.Name, "")
		fmt.Printf("Removed every second factor of %s and ended their sessions. They sign in with the password now and set up new ones.\n", u.Name)
	case c.createToken != "":
		u, err := store.UserByName(ctx, c.createToken)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.createToken, err)
		}
		scopes := scopeList(c.scopes)
		if len(scopes) == 0 {
			scopes = []string{"sync"}
		}
		if contains(scopes, "sync") && !u.Can(auth.ScopeSync) {
			return fmt.Errorf("%s's role (%s) may not sync, so their token can't either", u.Name, u.Role)
		}
		var expires time.Time
		if c.expiresDays > 0 {
			expires = time.Now().AddDate(0, 0, c.expiresDays)
		}
		token, err := store.CreateAPIToken(ctx, u, c.tokenName, scopes, expires)
		if err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "token_created", fmt.Sprintf("%s for %s (%s)", c.tokenName, u.Name, strings.Join(scopes, " ")), "")
		until := "never expires"
		if !expires.IsZero() {
			until = "expires " + expires.Format(time.DateOnly)
		}
		fmt.Printf("Token %q for %s (%s, %s). It is shown only now:\n\n%s\n\n", c.tokenName, u.Name, strings.Join(scopes, " "), until, token)
		fmt.Println(`Use it as "Authorization: Bearer <token>".`)
	case c.setRole != "":
		u, err := store.UserByName(ctx, c.setRole)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.setRole, err)
		}
		if err := store.SetRole(ctx, u, auth.Role(c.role)); err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "role_set", u.Name+" ("+c.role+")", "")
		fmt.Printf("%s has the role %s now.\n", u.Name, c.role)
	case c.createRole != "":
		if err := store.SaveRole(ctx, auth.Role(c.createRole), scopeList(c.scopes)); err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "role_saved", c.createRole+" ("+c.scopes+")", "")
		fmt.Printf("Saved the role %s. Give it to someone with -set-role <name> -role %q.\n", c.createRole, c.createRole)
	case c.deleteRole != "":
		if err := store.DeleteRole(ctx, auth.Role(c.deleteRole)); err != nil {
			return fmt.Errorf("role %q: %w", c.deleteRole, err)
		}
		store.Audit(ctx, nil, "cli", "role_deleted", c.deleteRole, "")
		fmt.Printf("Deleted the role %s.\n", c.deleteRole)
	case c.listRoles:
		return printRoles(ctx, store)
	}
	return nil
}

// printRoles lists the roles with their scopes and users, and every scope
// there is.
func printRoles(ctx context.Context, store *auth.Store) error {
	roles, err := store.Roles(ctx)
	if err != nil {
		return err
	}
	users, err := store.Users(ctx)
	if err != nil {
		return err
	}
	for _, r := range roles {
		kind := ""
		if r.Builtin {
			kind = " (built in)"
		}
		var names []string
		for _, u := range users {
			if u.Role == r.Name {
				names = append(names, u.Name)
			}
		}
		fmt.Printf("%s%s\n  scopes: %s\n  users:  %s\n", r.Name, kind, strings.Join(r.Scopes, " "), strings.Join(names, ", "))
	}
	fmt.Printf("\nScopes: %s (admin includes all others)\n", strings.Join(auth.AllScopes, " "))
	return nil
}

// newPassword asks for a password twice (without echo on a terminal) and
// checks it against the policy. Piped in, it is read from one line.
func newPassword(in *bufio.Reader, name string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		pw := strings.TrimRight(line, "\r\n")
		return pw, auth.ValidatePassword(pw, name)
	}
	for {
		fmt.Fprintf(os.Stderr, "Password for %s (at least %d characters): ", name, auth.MinPasswordLength)
		a, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if err := auth.ValidatePassword(string(a), name); err != nil {
			fmt.Fprintf(os.Stderr, "%v. Try another one.\n", err)
			continue
		}
		fmt.Fprint(os.Stderr, "Again: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			fmt.Fprintln(os.Stderr, "They differ. Once more.")
			continue
		}
		return string(a), nil
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
