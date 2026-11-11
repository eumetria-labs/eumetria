package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"eumetria/internal/auth"
)

// softKey is a passkey in software: an ES256 key whose signature counter
// the test sets, for what real authenticators do (or a copy of one).
type softKey struct {
	id     []byte
	priv   *ecdsa.PrivateKey
	handle []byte
}

var b64 = base64.RawURLEncoding

// newSoftKey makes a key and registers it as u's passkey (one that signs in
// on its own) with stored as its signature counter.
func newSoftKey(t *testing.T, e *testEnv, u *auth.User, stored uint32) *softKey {
	t.Helper()
	ctx := context.Background()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pt, _ := priv.PublicKey.ECDH()
	raw := pt.Bytes() // 0x04 || X || Y
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         int64(webauthncose.P256), XCoord: raw[1:33], YCoord: raw[33:],
	})
	if err != nil {
		t.Fatal(err)
	}
	k := &softKey{id: make([]byte, 16), priv: priv}
	rand.Read(k.id)
	cred := &webauthn.Credential{ID: k.id, PublicKey: cose, Authenticator: webauthn.Authenticator{SignCount: stored}}
	cred.Flags.UserPresent, cred.Flags.UserVerified = true, true
	if err := e.store.AddKey(ctx, u, "Soft key", cred, true); err != nil {
		t.Fatal(err)
	}
	if k.handle, err = e.store.WebAuthnHandle(ctx, u); err != nil {
		t.Fatal(err)
	}
	return k
}

// assert answers a passkey challenge with counter as the signature count.
func (k *softKey) assert(t *testing.T, challenge, origin, rpID string, counter uint32) string {
	t.Helper()
	client, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": origin})
	rp := sha256.Sum256([]byte(rpID))
	data := append(rp[:], 0x05) // user present, user verified
	data = binary.BigEndian.AppendUint32(data, counter)
	sum := sha256.Sum256(client)
	digest := sha256.Sum256(append(append([]byte{}, data...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON": b64.EncodeToString(client), "authenticatorData": b64.EncodeToString(data),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(k.handle),
		},
	})
	return string(out)
}

// passkeySignIn signs in with k alone and returns the status.
func (e *testEnv) passkeySignIn(t *testing.T, k *softKey, counter uint32) int {
	t.Helper()
	res, body := e.do(t, e.client(), "POST", "/api/login/passkey/begin", "{}", sameSite, host("localhost"))
	var begin struct {
		ID      string `json:"id"`
		Options struct {
			Challenge string `json:"challenge"`
		} `json:"options"`
	}
	if json.Unmarshal([]byte(body), &begin); res.StatusCode != 200 {
		t.Fatalf("begin = %d %s", res.StatusCode, body)
	}
	cred := k.assert(t, begin.Options.Challenge, "https://localhost", "localhost", counter)
	res, _ = e.do(t, e.client(), "POST", "/api/login/passkey/finish", `{"id":"`+begin.ID+`","credential":`+cred+`}`, sameSite, host("localhost"))
	return res.StatusCode
}

// A key whose counter doesn't go up may be a copy: refused, logged and
// shown to its user; keys that always send 0 keep working (pentest F14).
func TestClonedKeysAreRefused(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	anna, _ := e.store.UserByName(ctx, "anna")
	k := newSoftKey(t, e, anna, 10)
	for _, x := range []struct {
		counter uint32
		want    int
		why     string
	}{
		{11, 200, "counter went up"},
		{11, 401, "the same counter again"},
		{5, 401, "counter went back"},
		{0, 401, "counter back to 0"},
		{12, 200, "the real key, further on"},
	} {
		if got := e.passkeySignIn(t, k, x.counter); got != x.want {
			t.Errorf("%s (%d) = %d, want %d", x.why, x.counter, got, x.want)
		}
	}
	keys, _ := e.store.Keys(ctx, anna)
	if keys[0].Credential.Authenticator.SignCount != 12 {
		t.Errorf("stored counter = %d, want 12", keys[0].Credential.Authenticator.SignCount)
	}
	log, _ := e.store.AuditLog(ctx, 100)
	if !strings.Contains(jsonOf(t, log), "key_clone_warning") {
		t.Error("no key_clone_warning in the audit log")
	}
	if list, _ := e.store.Notices(ctx, anna, ""); len(list) == 0 || list[0].Kind != "key_clone_warning" {
		t.Errorf("notices = %+v", list)
	}

	// A synced passkey sends 0 every time: fine.
	bob, _ := e.store.CreateUser(ctx, "bob", coachPassword, auth.RoleViewer)
	synced := newSoftKey(t, e, bob, 0)
	for range 3 {
		if got := e.passkeySignIn(t, synced, 0); got != 200 {
			t.Errorf("synced passkey with 0 = %d, want 200", got)
		}
	}
}
