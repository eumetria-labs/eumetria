package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	sample "eumetria/internal/demo"
	"eumetria/internal/lifts"
	"eumetria/internal/mail"
	"eumetria/internal/settings"
	"eumetria/internal/wods"
)

// setupAccounts runs when the server starts. Without any account it serves
// no data and says how to make the first one; -demo makes one itself.
func setupAccounts(ctx context.Context, store *auth.Store, demo bool) error {
	has, err := store.HasUsers(ctx)
	if err != nil || has {
		return err
	}
	if demo {
		u, err := store.CreateDemoUser(ctx, "demo", "demo")
		if err != nil {
			return err
		}
		// Sample lifts, as -demo -out writes them for plain files.
		l := &lifts.Store{DB: store.DB, Now: store.Now}
		for _, e := range sample.Lifts(store.Now()) {
			in := lifts.Input{Lift: e.Lift, Kg: e.Kg, Reps: e.Reps, Date: e.Date, Note: e.Note}
			if _, err := l.Add(ctx, in, u.ID); err != nil {
				return err
			}
		}
		// And sample benchmark workouts.
		ws := &wods.Store{DB: store.DB, Now: store.Now}
		workouts, results := sample.Wods(store.Now())
		ids := map[int64]int64{}
		for _, w := range workouts {
			added, err := ws.AddWorkout(ctx, wods.WorkoutInput{Name: w.Name, Scoring: w.Scoring, TimeCap: w.TimeCap, Description: w.Description}, u.ID)
			if err != nil {
				return err
			}
			ids[w.ID] = added.ID
		}
		for _, r := range results {
			in := wods.ResultInput{Value: r.Value, Reps: r.Reps, Scaled: r.Scaled, Capped: r.Capped, FitbitWorkout: r.FitbitWorkout, Date: r.Date, Note: r.Note}
			if _, err := ws.AddResult(ctx, ids[r.Workout], in, u.ID); err != nil {
				return err
			}
		}
		log.Printf("demo: sign in as demo / demo")
		return nil
	}
	log.Printf("no account yet: create the first with -create-user <name> -role admin (README, \"Setup on a server\")")
	return nil
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
	resetFactors, enrolLink                string
	mail                                   bool   // with -enrol-link: also mail it
	email                                  string // with -create-user: invite at this address instead of asking for a password
	setRole, createRole, deleteRole        string
	listRoles                              bool
	role                                   string
	tokenName, scopes                      string
	expiresDays                            int
}

func (c accountCommand) any() bool {
	return c.createUser != "" || c.resetPassword != "" || c.createToken != "" || c.resetFactors != "" || c.enrolLink != "" ||
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
	case c.createUser != "" && c.email != "":
		return inviteCLI(ctx, store, c.createUser, auth.Role(c.role), c.email)
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
		if needsFactor(u, false) {
			return printEnrolLink(ctx, store, u, c.mail)
		}
	case c.resetPassword != "":
		u, err := store.UserByName(ctx, c.resetPassword)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.resetPassword, err)
		}
		pw, err := newPassword(in, u.Name)
		if err != nil {
			return err
		}
		if err := store.SetPassword(ctx, u, pw, 0, auth.Revoke{Tokens: true}); err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "password_reset", u.Name, "")
		fmt.Printf("New password set for %s; all of their sessions and API tokens have ended.\n", u.Name)
	case c.resetFactors != "":
		u, err := store.UserByName(ctx, c.resetFactors)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.resetFactors, err)
		}
		if err := store.ResetFactors(ctx, u); err != nil {
			return err
		}
		store.Audit(ctx, nil, "cli", "second_factors_reset", u.Name, "")
		store.AddNotice(ctx, u, "factors_reset", "", "", "")
		fmt.Printf("Removed every second factor of %s and ended their sessions. They sign in with the password now and set up new ones.\n", u.Name)
		if needsFactor(u, false) {
			return printEnrolLink(ctx, store, u, c.mail)
		}
	case c.enrolLink != "":
		u, err := store.UserByName(ctx, c.enrolLink)
		if err != nil {
			return fmt.Errorf("user %q: %w", c.enrolLink, err)
		}
		if f, err := store.Factors(ctx, u); err != nil {
			return err
		} else if f.Any() {
			return fmt.Errorf("%s has a second factor already; to start over, use -reset-second-factors", u.Name)
		}
		return printEnrolLink(ctx, store, u, c.mail)
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

// readSecret asks for a secret without echo on a terminal. Piped in, it is
// read from one line.
func readSecret(in *bufio.Reader, prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
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

// printEnrolLink makes an enrolment link for u (stepup.go) and prints it:
// their first second factor can only be set up with it.
func printEnrolLink(ctx context.Context, store *auth.Store, u *auth.User, send bool) error {
	token, expires, err := store.NewEnrolment(ctx, u)
	if err != nil {
		return err
	}
	store.Audit(ctx, nil, "cli", "enrol_link_made", u.Name, "")
	// The public URL is a setting anyone may read; no SETTINGS_KEY needed.
	var base string
	if conf, err := settings.New(store.DB, ""); err == nil {
		if v, err := conf.Values(ctx); err == nil {
			base = v.PublicURL
		}
	}
	fmt.Printf("\n%s needs a second factor and can set up the first one only with this link (once, until %s):\n\n%s\n\n",
		u.Name, expiresText(expires), enrolLink(base, token))
	fmt.Println("Hand it over separately from the password.")
	if base == "" {
		fmt.Println("(Set the public URL under Settings → General, or PUBLIC_URL, to have the whole link printed.)")
	}
	if send {
		return mailEnrolLinkCLI(ctx, store, u, enrolLink(base, token), expires)
	}
	return nil
}

// mailEnrolLinkCLI sends the link to u's confirmed address, now: the
// command says whether it worked. The mail server's password may be a
// sealed setting, so this needs SETTINGS_KEY in the environment.
func mailEnrolLinkCLI(ctx context.Context, store *auth.Store, u *auth.User, link string, expires time.Time) error {
	conf, err := settings.New(store.DB, os.Getenv("SETTINGS_KEY"))
	if err != nil {
		return err
	}
	v, err := conf.Values(ctx)
	if err != nil {
		return err
	}
	if !v.SMTP.Ready() {
		return errors.New("e-mail isn't set up (Settings → Email)")
	}
	addr, err := store.VerifiedEmail(ctx, u)
	if err != nil {
		return err
	}
	if addr == "" {
		return fmt.Errorf("%s has no confirmed e-mail address; hand the link over yourself", u.Name)
	}
	body := fmt.Sprintf("Your account %s needs a second factor. Set up the first one with this link: open it, sign in and add a passkey or an authenticator app. It works once, until %s. If you didn't expect this mail, ignore it and tell your admin.\n\n%s\n\n– %s\n",
		u.Name, expires.In(v.Zone).Format("2006-01-02 15:04"), link, v.Title)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := mailSender(v.SMTP).Send(ctx, mail.Message{To: addr, Subject: v.Title + ": Set up your second factor", Body: body}); err != nil {
		return fmt.Errorf("mailing the link: %w", err)
	}
	fmt.Printf("Sent it to %s.\n", addr)
	return nil
}

// inviteCLI makes an account for name and mails the invitation to addr now,
// instead of asking for a password: they choose it themselves (invite.go).
// The mail server's password may be a sealed setting: SETTINGS_KEY.
func inviteCLI(ctx context.Context, store *auth.Store, name string, role auth.Role, addr string) error {
	addr = strings.TrimSpace(addr)
	if mail.Address(addr) != nil {
		return fmt.Errorf("%q is no plain e-mail address", addr)
	}
	conf, err := settings.New(store.DB, os.Getenv("SETTINGS_KEY"))
	if err != nil {
		return err
	}
	v, err := conf.Values(ctx)
	if err != nil {
		return err
	}
	if !v.SMTP.Ready() {
		return errors.New("e-mail isn't set up (Settings → Email): create the account with a password instead")
	}
	// The command line has no request to take the site's address from.
	if v.PublicURL == "" {
		return errors.New("set the public URL first (Settings → General, or PUBLIC_URL): the link in the mail needs it")
	}
	u, err := store.CreateInvited(ctx, name, role)
	if err != nil {
		return err
	}
	token, expires, err := store.NewInvite(ctx, u, addr)
	if err == nil {
		err = store.SetEmail(ctx, u, addr)
	}
	if err != nil {
		return err
	}
	store.Audit(ctx, nil, "cli", "user_invited", u.Name+" ("+string(u.Role)+", "+addr+")", "")
	m := inviteMessage(v, u, addr, v.PublicURL+"/#invite="+token, expires, "An admin")
	sendCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := mailSender(v.SMTP).Send(sendCtx, m); err != nil {
		return fmt.Errorf("created %s, but mailing the invitation failed (send it again from Users & Roles): %w", u.Name, err)
	}
	fmt.Printf("Created %s (%s) and sent the invitation to %s. It works once, until %s.\n", u.Name, u.Role, addr, expiresText(expires))
	return nil
}
