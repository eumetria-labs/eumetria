// Package mail sends the app's few e-mails (security alerts, enrolment and
// confirmation links) over SMTP, always encrypted: implicit TLS (port 465)
// or STARTTLS (587), never plain text, and only with a valid certificate.
// Messages are plain text only, so nothing in them is ever HTML.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is one e-mail to one address.
type Message struct {
	To      string
	Subject string
	Body    string // plain text
}

// Sender sends a message; the queue (Outbox) calls it.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTP is a mail server to send through.
type SMTP struct {
	Host     string
	Port     int
	Implicit bool // TLS from the start (465); otherwise STARTTLS, which the server must offer
	User     string
	Password string
	From     string
	FromName string // shown with From, e.g. the site's title; may be ""
	// TLSConfig, if set, replaces the default (the system's roots, Host as
	// the server name); for tests.
	TLSConfig *tls.Config
}

var (
	ErrNoTLS      = errors.New("the mail server doesn't offer STARTTLS; refusing to send without encryption")
	ErrBadAddress = errors.New("not a plain e-mail address")
)

// Address checks that s is a plain address (no name, no line breaks).
func Address(s string) error {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || strings.ContainsAny(s, "\r\n") || len(s) > 254 {
		return ErrBadAddress
	}
	return nil
}

// Send delivers m through the server.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if err := Address(s.From); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	if err := Address(m.To); err != nil {
		return fmt.Errorf("to: %w", err)
	}
	data, err := Compose(s.From, s.FromName, m, time.Now())
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	cfg := s.TLSConfig
	if cfg == nil {
		cfg = &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	}
	d := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if s.Implicit {
		conn, err = (&tls.Dialer{NetDialer: d, Config: cfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(time.Minute))
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if !s.Implicit {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrNoTLS
		}
		if err := c.StartTLS(cfg); err != nil {
			return err
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return fmt.Errorf("%w: %w", ErrAuth, err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Compose writes m as a message from from, shown as name when that isn't
// empty ("Eumetria <noreply@…>"): UTF-8 plain text, quoted-printable, the
// subject and the name encoded, with a Date and a Message-ID.
func Compose(from, name string, m Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("a line break in the subject")
	}
	if strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return nil, errors.New("a control character in the sender's name")
	}
	id := make([]byte, 16)
	rand.Read(id)
	domain := from[strings.LastIndexByte(from, '@')+1:]
	var b strings.Builder
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	// net/mail quotes or encodes the name as needed; the envelope sender
	// (MAIL FROM) stays the bare address.
	header("From", (&mail.Address{Name: strings.TrimSpace(name), Address: from}).String())
	header("To", m.To)
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&crlf{&b})
	if _, err := io.WriteString(qp, m.Body); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// crlf turns the body's line ends into CRLF, as SMTP wants.
type crlf struct{ b *strings.Builder }

func (c *crlf) Write(p []byte) (int, error) {
	for _, ch := range p {
		if ch == '\n' {
			c.b.WriteByte('\r')
		}
		if ch != '\r' {
			c.b.WriteByte(ch)
		}
	}
	return len(p), nil
}
