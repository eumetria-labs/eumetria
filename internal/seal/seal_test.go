package seal

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	e, err := SealJSON(map[string]string{"title": "Gains"}, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	// Through JSON, as the page receives it.
	raw, _ := json.Marshal(e)
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	plain, err := Open(&back, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"title":"Gains"}` {
		t.Errorf("got %s", plain)
	}
	if _, err := Open(&back, "wrong horse"); err == nil {
		t.Error("wrong password opened the envelope")
	}
}

func TestFreshIV(t *testing.T) {
	a, _ := Seal([]byte("x"), "pw")
	b, _ := Seal([]byte("x"), "pw")
	if string(a.IV) == string(b.IV) {
		t.Error("two seals share an IV")
	}
}

func TestEmptyPassword(t *testing.T) {
	if _, err := Seal([]byte("x"), ""); err == nil {
		t.Error("sealed with an empty password")
	}
}

// The browser test (tests/lock.test.js) decrypts the same fixtures, so both
// sides are checked against one file. sealed.json is from before
// compression, sealed-gzip.json from Seal as it is now.
func TestFixture(t *testing.T) {
	for _, name := range []string{"sealed.json", "sealed-gzip.json"} {
		raw, err := os.ReadFile("../../tests/fixtures/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var e Envelope
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		plain, err := Open(&e, "gym bros only")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(plain) != `{"title":"Fixture"}` {
			t.Errorf("%s: got %s", name, plain)
		}
	}
}

func TestCompresses(t *testing.T) {
	plain := bytes.Repeat([]byte(`{"date":"2026-10-22","energy":2100},`), 200)
	e, err := Seal(plain, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if e.Zip != "gzip" || len(e.Data) > len(plain)/4 {
		t.Errorf("zip=%q, %d bytes sealed from %d", e.Zip, len(e.Data), len(plain))
	}
}
