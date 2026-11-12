package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/textproto"
	"syscall"
)

// ErrAuth wraps a refused sign-in at the mail server.
var ErrAuth = errors.New("the mail server refused the user name or password")

// Category sorts a sending error into a few kinds, for the admin page:
// dns, refused, timeout, tls, auth, rejected, not_set_up or other ("" for
// nil). The full error belongs in the server log only. Shown as it is, it
// would let whoever may change the mail server probe what the container
// reaches, host by host and port by port (pentest re-test I4).
func Category(err error) string {
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var proto *textproto.Error
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotSetUp):
		return "not_set_up"
	case errors.Is(err, ErrAuth):
		return "auth"
	case errors.Is(err, ErrNoTLS), errors.As(err, &cert), errors.As(err, &unknownAuthority), errors.As(err, &hostname),
		errors.As(err, &invalid), errors.As(err, &record), errors.As(err, &alert):
		return "tls"
	case errors.As(err, &dns):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.As(err, &proto):
		return "rejected"
	}
	return "other"
}
