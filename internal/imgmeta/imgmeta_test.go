package imgmeta

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		img.Set(x, x, color.RGBA{200, 50, 50, 255})
	}
	return img
}

// exifSegment builds an APP1 EXIF block with an orientation tag and a GPS IFD pointer.
func exifSegment(orientation int) []byte {
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 2, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0, // orientation
		0x25, 0x88, 4, 0, 1, 0, 0, 0, 38, 0, 0, 0, // GPS IFD pointer
		0, 0, 0, 0}
	tiff = append(tiff, []byte("GPS 52.5200N 13.4050E secret-location")...)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func TestStripJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(), nil); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	comment := []byte{0xFF, 0xFE, 0, 12, 'm', 'y', ' ', 'k', 'i', 't', 'c', 'h', 'e', 'n'}
	in := append(append(append([]byte{}, plain[:2]...), append(exifSegment(6), comment...)...), plain[2:]...)

	out, err := Strip(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"secret-location", "my kitchen"} {
		if bytes.Contains(out, []byte(leak)) {
			t.Errorf("%q survived stripping", leak)
		}
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped JPEG does not decode: %v", err)
	}
	// The rotation survives as a minimal EXIF block.
	if i := bytes.Index(out, []byte{0xFF, 0xE1}); i < 0 || exifOrientation(out[i+4:]) != 6 {
		t.Error("orientation 6 was not kept")
	}

	// An upright photo needs no EXIF at all.
	out, _ = Strip(append(append(append([]byte{}, plain[:2]...), exifSegment(1)...), plain[2:]...))
	if bytes.Contains(out, []byte("Exif")) {
		t.Error("EXIF kept for an upright photo")
	}
}

func TestStripPNG(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage()); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	data := []byte("Comment\x00taken at home")
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	in := append(append(append([]byte{}, plain[:33]...), chunk...), plain[33:]...) // after IHDR

	out, err := Strip(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("taken at home")) {
		t.Error("tEXt chunk survived")
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped PNG does not decode: %v", err)
	}
}

func TestStripWebP(t *testing.T) {
	chunk := func(id string, data []byte) []byte {
		c := append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(data)))...)
		c = append(c, data...)
		if len(data)%2 == 1 {
			c = append(c, 0)
		}
		return c
	}
	body := chunk("VP8X", []byte{0x08 | 0x04, 0, 0, 0, 7, 0, 0, 7, 0, 0})
	body = append(body, chunk("VP8L", []byte{1, 2, 3})...)
	body = append(body, chunk("EXIF", []byte("GPS secret"))...)
	in := append([]byte("RIFF\x00\x00\x00\x00WEBP"), body...)
	binary.LittleEndian.PutUint32(in[4:], uint32(len(in)-8))

	out, err := Strip(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS secret")) || bytes.Contains(out, []byte("EXIF")) {
		t.Error("EXIF chunk survived")
	}
	if got := binary.LittleEndian.Uint32(out[4:]); int(got) != len(out)-8 {
		t.Errorf("RIFF size %d, want %d", got, len(out)-8)
	}
	if out[20]&0x0C != 0 {
		t.Errorf("VP8X still flags EXIF/XMP: %#x", out[20])
	}
}

func TestStripRejectsUnknownAndBroken(t *testing.T) {
	for name, in := range map[string][]byte{
		"gif":       []byte("GIF89a..."),
		"truncated": {0xFF, 0xD8, 0xFF, 0xE1, 0xFF},
	} {
		if _, err := Strip(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// The real photos from the sync cache, if this checkout has any.
func TestStripCachedPhotos(t *testing.T) {
	files, _ := filepath.Glob("../../data/photos/*")
	if len(files) == 0 {
		t.Skip("no cached photos")
	}
	for _, f := range files {
		in, _ := os.ReadFile(f)
		out, err := Strip(in)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
			t.Errorf("%s: stripped photo does not decode: %v", f, err)
		}
	}
}
