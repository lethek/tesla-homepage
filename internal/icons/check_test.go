package icons

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	imgpng "image/png"
	"testing"
)

// Real files from Google's WebP feature-detection snippet: 1x1 lossy,
// lossless, lossless with alpha, and an animated one.
var webps = map[string]string{
	"lossy":    "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA",
	"lossless": "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==",
	"alpha":    "UklGRkoAAABXRUJQVlA4WAoAAAAQAAAAAAAAAAAAQUxQSAwAAAARBxAR/Q9ERP8DAABWUDggGAAAABQBAJ0BKgEAAQAAAP4AAA3AAP7mtQAAAA==",
	"animated": "UklGRlIAAABXRUJQVlA4WAoAAAASAAAAAAAAAAAAQU5JTQYAAAD/////AABBTk1GJgAAAAAAAAAAAAAAAAAAAGQAAABWUDhMDQAAAC8AAAAQBxAREYiI/gcA",
}

func webpFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(webps[name])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func encode(t *testing.T, enc func(*bytes.Buffer, image.Image) error, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := enc(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngBytes(t *testing.T, w, h int) []byte {
	return encode(t, func(b *bytes.Buffer, i image.Image) error { return imgpng.Encode(b, i) }, w, h)
}

func jpegBytes(t *testing.T, w, h int) []byte {
	return encode(t, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }, w, h)
}

func gifBytes(t *testing.T) []byte {
	return encode(t, func(b *bytes.Buffer, i image.Image) error { return gif.Encode(b, i, nil) }, 4, 4)
}

// icoWith wraps one image in an ICO file.
func icoWith(img []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 1, 0, 1, 0})
	entry := make([]byte, 16)
	entry[0], entry[1], entry[4], entry[6] = 1, 1, 1, 32
	binary.LittleEndian.PutUint32(entry[8:], uint32(len(img)))
	binary.LittleEndian.PutUint32(entry[12:], 22)
	b.Write(entry)
	b.Write(img)
	return b.Bytes()
}

func TestCheckAcceptsWholeImages(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		ext  string
	}{
		"png":             {pngBytes(t, 8, 8), ".png"},
		"jpeg":            {jpegBytes(t, 8, 8), ".jpg"},
		"gif":             {gifBytes(t), ".gif"},
		"webp lossy":      {webpFile(t, "lossy"), ".webp"},
		"webp lossless":   {webpFile(t, "lossless"), ".webp"},
		"webp alpha":      {webpFile(t, "alpha"), ".webp"},
		"webp animated":   {webpFile(t, "animated"), ".webp"},
		"ico with png":    {icoWith(pngBytes(t, 8, 8)), ".ico"},
		"ico with bmp":    {icoWith(make([]byte, 40)), ".ico"},
		"svg":             {[]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`), ".svg"},
		"svg with entity": {[]byte(`<!DOCTYPE svg [<!ENTITY ns "http://x">]><svg xmlns="&ns;"><g/></svg>`), ".svg"},
		"png then junk":   {append(pngBytes(t, 8, 8), "trailing"...), ".png"},
	} {
		if ext, err := Check(tc.data); err != nil || ext != tc.ext {
			t.Errorf("%s: got %q, %v, want %q", name, ext, err, tc.ext)
		}
	}
}

func TestCheckRefusesDamagedImages(t *testing.T) {
	p, j, w := pngBytes(t, 64, 64), jpegBytes(t, 64, 64), webpFile(t, "lossless")
	corruptMiddle := append([]byte{}, p...)
	for i := len(corruptMiddle) / 2; i < len(corruptMiddle)/2+8; i++ {
		corruptMiddle[i] ^= 0xFF
	}
	badEntry := icoWith(pngBytes(t, 8, 8))
	binary.LittleEndian.PutUint32(badEntry[14:], 9999) // offset past the end of the file
	for name, data := range map[string][]byte{
		"png truncated":     p[:len(p)-20],
		"png corrupt data":  corruptMiddle,
		"png header only":   p[:33],
		"jpeg truncated":    j[:len(j)-10],
		"gif truncated":     gifBytes(t)[:len(gifBytes(t))-8],
		"webp truncated":    w[:len(w)-4],
		"webp animated cut": webpFile(t, "animated")[:40],
		"ico bad offset":    badEntry,
		"ico no images":     {0, 0, 1, 0, 0, 0},
		"svg truncated":     []byte(`<svg xmlns="http://www.w3.org/2000/svg"><g><path d="M0 0"/>`),
		"svg not svg root":  []byte(`<?xml version="1.0"?><note><svg/></note>`),
	} {
		if _, err := Check(data); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: got %v, want ErrCorrupt", name, err)
		}
	}
}

func TestCheckRefusesUnsupportedAndOversize(t *testing.T) {
	if _, err := Check([]byte("hello")); !errors.Is(err, ErrNotImage) {
		t.Errorf("text: %v", err)
	}
	// A tiny file claiming huge dimensions must be refused without decoding it.
	big := pngBytes(t, 1, 1)
	binary.BigEndian.PutUint32(big[16:], 20000)                          // width in the IHDR chunk
	binary.BigEndian.PutUint32(big[20:], 20000)                          // height
	binary.BigEndian.PutUint32(big[29:], crc32.ChecksumIEEE(big[12:29])) // the chunk's checksum
	if _, err := Check(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversize png: %v", err)
	}
	huge := append([]byte{}, webpFile(t, "animated")...)
	copy(huge[24:], []byte{0xFF, 0x0F, 0x00, 0xFF, 0x0F, 0x00}) // 4096 x 4096 canvas is fine
	if _, err := Check(huge); err != nil {
		t.Errorf("4096x4096 animated webp: %v", err)
	}
	copy(huge[24:], []byte{0xFF, 0xFF, 0x00, 0xFF, 0xFF, 0x00}) // 65536 x 65536
	if _, err := Check(huge); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversize animated webp: %v", err)
	}
}

func TestFromUploadRefusesDamagedImage(t *testing.T) {
	r, _ := newResolver(t)
	p := pngBytes(t, 32, 32)
	if _, err := r.FromUpload(p[:len(p)-20]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated png: %v", err)
	}
}
