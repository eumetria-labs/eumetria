// Package imgmeta removes metadata (EXIF with GPS position and camera,
// XMP, IPTC, comments) from JPEG, PNG and WebP files without re-encoding
// the image, so published photos cannot reveal where or when they were taken.
package imgmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Strip returns a copy of img without metadata. JPEGs keep their colour
// profile and, if the photo is rotated, a minimal EXIF block holding only the
// orientation, so it still displays upright. Unknown formats are an error, so
// callers never publish a file they could not clean.
func Strip(img []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(img, []byte{0xFF, 0xD8}):
		return stripJPEG(img)
	case bytes.HasPrefix(img, []byte("\x89PNG\r\n\x1a\n")):
		return stripPNG(img)
	case len(img) >= 12 && string(img[:4]) == "RIFF" && string(img[8:12]) == "WEBP":
		return stripWebP(img)
	}
	return nil, errors.New("imgmeta: unsupported image format")
}

var errTruncated = errors.New("imgmeta: truncated image")

func stripJPEG(img []byte) ([]byte, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(img)))
	out.Write(img[:2]) // SOI
	orientation := 0
	for i := 2; ; {
		if i+4 > len(img) || img[i] != 0xFF {
			return nil, errTruncated
		}
		marker := img[i+1]
		if marker == 0xDA { // start of scan: the rest is image data
			if orientation > 1 {
				out.Write(orientationEXIF(orientation))
			}
			out.Write(img[i:])
			return out.Bytes(), nil
		}
		n := int(binary.BigEndian.Uint16(img[i+2:]))
		end := i + 2 + n
		if n < 2 || end > len(img) {
			return nil, errTruncated
		}
		seg := img[i:end]
		switch {
		case marker == 0xE1: // APP1: EXIF or XMP
			if o := exifOrientation(seg[4:]); o > 0 {
				orientation = o
			}
		case marker >= 0xE3 && marker <= 0xEF, marker == 0xFE: // other APPn (IPTC, Photoshop, …) and comments
		default: // APP0 (JFIF), APP2 (ICC colour profile) and image structure
			out.Write(seg)
		}
		i = end
	}
}

// exifOrientation reads tag 0x0112 from an APP1 payload ("Exif\0\0" + TIFF).
func exifOrientation(p []byte) int {
	if len(p) < 14 || string(p[:6]) != "Exif\x00\x00" {
		return 0
	}
	t := p[6:]
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 0
	}
	count := int(bo.Uint16(t[off:]))
	for k := range count {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			return int(bo.Uint16(t[e+8:]))
		}
	}
	return 0
}

// orientationEXIF builds an APP1 segment whose only tag is the orientation.
func orientationEXIF(o int) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, // header, IFD at 8, one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, byte(o), 0, 0, // orientation, SHORT, count 1, value
		0, 0, 0, 0} // no next IFD
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func stripPNG(img []byte) ([]byte, error) {
	drop := map[string]bool{"tEXt": true, "zTXt": true, "iTXt": true, "eXIf": true, "tIME": true}
	out := bytes.NewBuffer(make([]byte, 0, len(img)))
	out.Write(img[:8])
	for i := 8; i < len(img); {
		if i+12 > len(img) {
			return nil, errTruncated
		}
		n := int(binary.BigEndian.Uint32(img[i:]))
		end := i + 12 + n
		if end > len(img) {
			return nil, errTruncated
		}
		if !drop[string(img[i+4:i+8])] {
			out.Write(img[i:end])
		}
		i = end
	}
	return out.Bytes(), nil
}

func stripWebP(img []byte) ([]byte, error) {
	body := bytes.NewBuffer(nil)
	for i := 12; i < len(img); {
		if i+8 > len(img) {
			return nil, errTruncated
		}
		id := string(img[i : i+4])
		n := int(binary.LittleEndian.Uint32(img[i+4:]))
		end := i + 8 + n + n%2 // chunks are padded to an even size
		if end > len(img) {
			return nil, errTruncated
		}
		chunk := append([]byte(nil), img[i:end]...)
		switch id {
		case "EXIF", "XMP ":
			i = end
			continue
		case "VP8X":
			if len(chunk) > 8 {
				chunk[8] &^= 0x08 | 0x04 // clear the EXIF and XMP flags
			}
		}
		body.Write(chunk)
		i = end
	}
	out := append([]byte("RIFF\x00\x00\x00\x00WEBP"), body.Bytes()...)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out, nil
}
