// Package picture handles images people upload. Decode and JPEG go through
// the pixels and write a new file, which leaves metadata, and anything hidden
// in the upload, behind.
package picture

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // what a pasted screenshot may be
	"os"
	"path/filepath"
	"time"
)

// MaxSide is the longest side taken; the page scales images down to less. It
// also keeps a small file from decoding into a huge image.
const MaxSide = 4096

// Pixel budgets. A decoded image takes 4 bytes per pixel twice over (the
// decoder's copy and the flattened one), so these keep one upload at a few
// dozen MB however well its file compresses.
const (
	AvatarPixels = 4_000_000  // avatars end up 512 px square
	NotePixels   = 12_000_000 // a 12 MP photo
)

var (
	ErrBad  = errors.New("not a usable image")
	ErrBusy = errors.New("too many images at once")
)

// one image is worked on at a time: decoding is what needs the memory, and
// the container has little of it.
var (
	slot     = make(chan struct{}, 1)
	slotWait = 20 * time.Second
)

// Clean decodes raw (Decode), hands the pixels to shape when it isn't nil and
// writes the result as a new JPEG, one image at a time. It gives up with
// ErrBusy when others keep it waiting too long.
func Clean(raw []byte, maxPixels int, shape func(*image.RGBA) *image.RGBA) ([]byte, error) {
	select {
	case slot <- struct{}{}:
	case <-time.After(slotWait):
		return nil, ErrBusy
	}
	defer func() { <-slot }()
	img, err := Decode(raw, maxPixels)
	if err != nil {
		return nil, err
	}
	if shape != nil {
		img = shape(img)
	}
	return JPEG(img)
}

// Decode reads a JPEG or PNG of at most maxPixels pixels and 8 bits per
// channel, on white where it is transparent. Anything else is ErrBad,
// whatever its name or type claim: a 16-bit PNG would decode into twice the
// memory, from a file that compresses to almost nothing.
func Decode(raw []byte, maxPixels int) (*image.RGBA, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, ErrBad
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxSide || cfg.Height > MaxSide ||
		cfg.Width*cfg.Height > maxPixels {
		return nil, ErrBad
	}
	switch cfg.ColorModel {
	case color.RGBA64Model, color.NRGBA64Model, color.Gray16Model, color.Alpha16Model:
		return nil, ErrBad
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrBad
	}
	flat := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, src.Bounds().Min, draw.Over)
	return flat, nil
}

// JPEG encodes img as a new file.
func JPEG(img image.Image) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Square cuts the largest centred square out of img and scales it down to
// side px, each new pixel the average of the ones it covers. A smaller square
// keeps its size: enlarging only blurs it.
func Square(img *image.RGBA, side int) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	from := min(w, h)
	x0, y0 := (w-from)/2, (h-from)/2
	side = min(side, from)
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			var r, g, b, n int
			for sy := y * from / side; sy < max((y+1)*from/side, y*from/side+1); sy++ {
				for sx := x * from / side; sx < max((x+1)*from/side, x*from/side+1); sx++ {
					c := img.RGBAAt(x0+sx, y0+sy)
					r, g, b, n = r+int(c.R), g+int(c.G), b+int(c.B), n+1
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255})
		}
	}
	return out
}

// Save writes data as dir/name in one step, so nobody reads half a file.
func Save(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
