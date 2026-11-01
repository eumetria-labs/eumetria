package picture

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
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
	img, err := Decode(encode(t, image.NewNRGBA(image.Rect(0, 0, 4, 2))))
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
	} {
		if _, err := Decode(raw); !errors.Is(err, ErrBad) {
			t.Errorf("%s: %v", name, err)
		}
	}
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
