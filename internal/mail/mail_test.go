package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompose(t *testing.T) {
	data, err := Compose("eumetria@example.org", "", Message{To: "anna@example.org", Subject: "Neuer Schlüssel", Body: "Zeile eins\nZeile zwei = drei"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"From: <eumetria@example.org>\r\n", "To: anna@example.org\r\n", "Subject: =?utf-8?q?Neuer_Schl=C3=BCssel?=\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n", "Zeile eins\r\nZeile zwei =3D drei", "Message-ID: <"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if _, err := Compose("a@example.org", "", Message{To: "b@example.org", Subject: "x\r\nBcc: evil@example.org"}, time.Now()); err == nil {
		t.Error("a line break in the subject went through")
	}
	// A name in front of the address, encoded when it isn't plain ASCII;
	// line breaks in it are refused.
	for name, want := range map[string]string{
		"Eumetria":              "From: \"Eumetria\" <eumetria@example.org>\r\n",
		"Eumetria - LOCAL DEV":  "From: \"Eumetria - LOCAL DEV\" <eumetria@example.org>\r\n",
		"Eumetria · Gesundheit": "From: =?utf-8?q?Eumetria_=C2=B7_Gesundheit?= <eumetria@example.org>\r\n",
	} {
		data, err := Compose("eumetria@example.org", name, Message{To: "b@example.org", Subject: "x"}, time.Now())
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("name %q: %v\n%s", name, err, data)
		}
	}
	if _, err := Compose("a@example.org", "Eumetria\r\nBcc: evil@example.org", Message{To: "b@example.org", Subject: "x"}, time.Now()); err == nil {
		t.Error("a line break in the name went through")
	}
}

func TestAddress(t *testing.T) {
	for _, bad := range []string{"", "anna", "Anna <anna@example.org>", "anna@example.org\r\nBcc: x@example.org"} {
		if Address(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if Address("anna@example.org") != nil {
		t.Error("a plain address refused")
	}
}

// fakeServer speaks just enough SMTP; with tlsCfg it offers STARTTLS.
type fakeServer struct {
	ln     net.Listener
	tlsCfg *tls.Config
	mu     sync.Mutex
	got    []string // DATA of each message
	auth   []string
}

func newFake(t *testing.T, tlsCfg *tls.Config) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, tlsCfg: tlsCfg}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeServer) serve(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), c
	say := func(s string) { w.Write([]byte(s + "\r\n")) }
	say("220 fake")
	secure := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			if f.tlsCfg != nil && !secure {
				say("250-fake")
				say("250 STARTTLS")
			} else {
				say("250-fake")
				say("250 AUTH PLAIN")
			}
		case cmd == "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, f.tlsCfg)
			if tc.Handshake() != nil {
				return
			}
			c, secure = tc, true
			r, w = bufio.NewReader(tc), tc
		case strings.HasPrefix(cmd, "AUTH"):
			f.mu.Lock()
			f.auth = append(f.auth, strings.TrimSpace(line))
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.got = append(f.got, b.String())
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func testCert(t *testing.T) (*tls.Config, *tls.Config) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	server := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	client := &tls.Config{ServerName: "localhost", RootCAs: pool}
	return server, client
}

func port(f *fakeServer) int { return f.ln.Addr().(*net.TCPAddr).Port }

func TestSendNeedsTLS(t *testing.T) {
	ctx := context.Background()
	m := Message{To: "anna@example.org", Subject: "Hi", Body: "Hello"}
	// A server without STARTTLS gets nothing: no password, no message.
	plain := newFake(t, nil)
	s := &SMTP{Host: "localhost", Port: port(plain), User: "u", Password: "secret", From: "eumetria@example.org"}
	if err := s.Send(ctx, m); !errors.Is(err, ErrNoTLS) {
		t.Errorf("without STARTTLS: %v", err)
	}
	if len(plain.got) != 0 || len(plain.auth) != 0 {
		t.Error("sent over plain text")
	}
	// With it, the message goes through, after the upgrade.
	serverCfg, clientCfg := testCert(t)
	secure := newFake(t, serverCfg)
	s = &SMTP{Host: "localhost", Port: port(secure), User: "u", Password: "secret", From: "eumetria@example.org", TLSConfig: clientCfg}
	if err := s.Send(ctx, m); err != nil {
		t.Fatal(err)
	}
	if len(secure.got) != 1 || !strings.Contains(secure.got[0], "Subject: Hi") || len(secure.auth) != 1 {
		t.Errorf("got %q, auth %q", secure.got, secure.auth)
	}
	// A certificate the client doesn't trust: refused.
	s.TLSConfig = nil
	if err := s.Send(ctx, m); err == nil {
		t.Error("an untrusted certificate was accepted")
	}
}

type fakeSender struct {
	mu   sync.Mutex
	fail int
	sent []Message
}

func (f *fakeSender) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("try again")
	}
	f.sent = append(f.sent, m)
	return nil
}

func TestOutboxRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeSender{fail: 2}
	o := &Outbox{Sender: func(context.Context) Sender { return f }, Pause: []time.Duration{time.Millisecond}}
	o.Start(ctx)
	o.Queue(Message{To: "a@example.org", Subject: "x"})
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		n := len(f.sent)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(f.sent) != 1 || o.Status().LastSent.IsZero() {
		t.Fatalf("sent %d, status %+v", len(f.sent), o.Status())
	}
	// Failing every time: given up, and the status says why.
	f.mu.Lock()
	f.fail = 10
	f.mu.Unlock()
	o.Queue(Message{To: "b@example.org", Subject: "y"})
	for i := 0; i < 100 && o.Status().LastError == ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if o.Status().LastError != "try again" {
		t.Errorf("status = %+v", o.Status())
	}
}
