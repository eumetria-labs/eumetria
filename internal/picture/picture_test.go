package picture

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
	"time"
)

func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecode(t *testing.T) {
	img, err := Decode(encode(t, image.NewNRGBA(image.Rect(0, 0, 4, 2))), NotePixels)
	if err != nil || img.Bounds().Dx() != 4 || img.Bounds().Dy() != 2 {
		t.Fatalf("decode = %v, %v", img, err)
	}
	if c := img.RGBAAt(1, 1); c.R != 255 || c.A != 255 {
		t.Errorf("a transparent pixel = %v, want white", c)
	}
	var anim bytes.Buffer
	if err := gif.Encode(&anim, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"text":   []byte("<svg onload=alert(1)>"),
		"gif":    anim.Bytes(),
		"empty":  nil,
		"broken": encode(t, image.NewGray(image.Rect(0, 0, 8, 8)))[:40],
		"huge":   encode(t, image.NewGray(image.Rect(0, 0, MaxSide+1, 1))),
		// Within MaxSide, but more pixels than an avatar may have.
		"many": encode(t, image.NewGray(image.Rect(0, 0, 2001, 2000))),
		// 16 bits per channel: the decompression bomb of the pentest (F9)
		// is a 4096² one of these, 130 KB as a file.
		"16-bit":      encode(t, image.NewNRGBA64(image.Rect(0, 0, 8, 8))),
		"16-bit grey": encode(t, image.NewGray16(image.Rect(0, 0, 8, 8))),
	} {
		if _, err := Decode(raw, AvatarPixels); !errors.Is(err, ErrBad) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestClean(t *testing.T) {
	h, err := Acquire()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Clean(h, encode(t, image.NewRGBA(image.Rect(0, 0, 40, 20))), AvatarPixels, func(img *image.RGBA) *image.RGBA {
		return Square(img, 10)
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "jpeg" || cfg.Width != 10 || cfg.Height != 10 {
		t.Errorf("clean = %s %dx%d, %v", format, cfg.Width, cfg.Height, err)
	}
	// Only one at a time: with the slot taken, the next one waits, then gives up.
	wait := slotWait
	slotWait = 10 * time.Millisecond
	defer func() { slotWait = wait }()
	if _, err := Acquire(); !errors.Is(err, ErrBusy) {
		t.Errorf("with the slot taken: %v", err)
	}
	h.Release()
	h.Release() // twice is fine
	h2, err := Acquire()
	if err != nil {
		t.Errorf("after release: %v", err)
	}
	h2.Release()
}

func TestSquare(t *testing.T) {
	// Red, then green in the middle, then blue: a wide picture keeps its middle.
	wide := image.NewRGBA(image.Rect(0, 0, 900, 300))
	for x := range 900 {
		for y := range 300 {
			c := color.RGBA{0, 255, 0, 255}
			if x < 300 {
				c = color.RGBA{255, 0, 0, 255}
			} else if x >= 600 {
				c = color.RGBA{0, 0, 255, 255}
			}
			wide.SetRGBA(x, y, c)
		}
	}
	out := Square(wide, 100)
	if out.Bounds().Dx() != 100 || out.Bounds().Dy() != 100 {
		t.Fatalf("size = %v", out.Bounds())
	}
	for _, p := range []image.Point{{0, 0}, {99, 99}, {50, 50}} {
		if c := out.RGBAAt(p.X, p.Y); c != (color.RGBA{0, 255, 0, 255}) {
			t.Errorf("%v = %v, want the middle's green", p, c)
		}
	}
	// A tall one too, and a small one stays small.
	if b := Square(image.NewRGBA(image.Rect(0, 0, 300, 900)), 512).Bounds(); b.Dx() != 300 || b.Dy() != 300 {
		t.Errorf("tall = %v", b)
	}
	if b := Square(image.NewRGBA(image.Rect(0, 0, 1, 1)), 512).Bounds(); b.Dx() != 1 {
		t.Errorf("one pixel = %v", b)
	}
	// Scaling down averages: stripes turn grey instead of keeping every other line.
	stripes := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := range 8 {
		for y := range 8 {
			stripes.SetRGBA(x, y, color.RGBA{uint8(255 * (x % 2)), 0, 0, 255})
		}
	}
	if c := Square(stripes, 4).RGBAAt(1, 1); c.R < 120 || c.R > 135 {
		t.Errorf("an averaged pixel = %v", c)
	}
}
