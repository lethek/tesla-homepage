package icons

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	imgpng "image/png"
	"io"

	"golang.org/x/image/webp"
)

// maxPixels is the largest image accepted, in pixels. Decoding needs memory
// for every pixel, and a small file can claim huge dimensions.
const maxPixels = 16 << 20

var (
	ErrCorrupt  = errors.New("the image file is damaged or incomplete")
	ErrTooLarge = errors.New("the image's dimensions are too large (over 16 megapixels)")
)

// Check decides whether data is a usable image and returns the file extension
// for its type. It sniffs the type from the bytes, then decodes the whole
// image so damaged or truncated files are refused. ErrNotImage means the type
// isn't supported, ErrCorrupt that it is but can't be read, and ErrTooLarge
// that its dimensions are over the limit.
func Check(data []byte) (string, error) {
	ext, ok := imageExt(data)
	if !ok {
		return "", ErrNotImage
	}
	var err error
	switch ext {
	case ".png":
		err = decode(data, imgpng.DecodeConfig, imgpng.Decode)
	case ".jpg":
		err = decode(data, jpeg.DecodeConfig, jpeg.Decode)
	case ".gif":
		// Decodes the first frame, which is the image shown as an icon.
		err = decode(data, gif.DecodeConfig, gif.Decode)
	case ".webp":
		err = checkWebP(data)
	case ".ico":
		err = checkICO(data)
	case ".svg":
		err = checkSVG(data)
	}
	if err != nil {
		return "", err
	}
	return ext, nil
}

// decode reads the image's size first, so an oversize image is refused before
// any memory is spent on its pixels, then decodes all of it.
func decode(data []byte, config func(io.Reader) (image.Config, error), full func(io.Reader) (image.Image, error)) error {
	c, err := config(bytes.NewReader(data))
	if err != nil {
		return ErrCorrupt
	}
	if err := checkSize(c.Width, c.Height); err != nil {
		return err
	}
	if _, err := full(bytes.NewReader(data)); err != nil {
		return ErrCorrupt
	}
	return nil
}

func checkSize(w, h int) error {
	switch {
	case w <= 0 || h <= 0:
		return ErrCorrupt
	case int64(w)*int64(h) > maxPixels:
		return ErrTooLarge
	}
	return nil
}

// checkWebP decodes a still WebP. Animated files aren't supported by the
// decoder, so for those the file's structure is checked instead.
func checkWebP(data []byte) error {
	end, err := riffEnd(data)
	if err != nil {
		return err
	}
	const flagsAt, animationBit = 20, 1 << 1
	if len(data) > flagsAt && string(data[12:16]) == "VP8X" && data[flagsAt]&animationBit != 0 {
		if end < 30 {
			return ErrCorrupt
		}
		w := (int(data[24]) | int(data[25])<<8 | int(data[26])<<16) + 1
		h := (int(data[27]) | int(data[28])<<8 | int(data[29])<<16) + 1
		return checkSize(w, h)
	}
	return decode(data, webp.DecodeConfig, webp.Decode)
}

// riffEnd checks a RIFF file's chunks end exactly where the file says they do,
// which catches truncation. It returns the offset where the chunks end.
func riffEnd(data []byte) (int, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, ErrCorrupt
	}
	size := int64(binary.LittleEndian.Uint32(data[4:8]))
	end := size + 8
	if size < 4 || end > int64(len(data)) {
		return 0, ErrCorrupt
	}
	off := int64(12)
	for off < end {
		if off+8 > end {
			return 0, ErrCorrupt
		}
		n := int64(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		off += 8 + n + n&1
		if off > end {
			return 0, ErrCorrupt
		}
	}
	return int(end), nil
}

// checkICO checks an icon file's directory points at data inside the file, and
// decodes any image in it that is stored as PNG.
func checkICO(data []byte) error {
	const header, entry = 6, 16
	if len(data) < header {
		return ErrCorrupt
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || header+count*entry > len(data) {
		return ErrCorrupt
	}
	for i := range count {
		e := data[header+i*entry : header+(i+1)*entry]
		size := int64(binary.LittleEndian.Uint32(e[8:12]))
		off := int64(binary.LittleEndian.Uint32(e[12:16]))
		if size == 0 || off < int64(header+count*entry) || off+size > int64(len(data)) {
			return ErrCorrupt
		}
		img := data[off : off+size]
		if bytes.HasPrefix(img, []byte("\x89PNG\r\n\x1a\n")) {
			if err := decode(img, imgpng.DecodeConfig, imgpng.Decode); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSVG checks the file is well-formed XML whose root element is svg.
func checkSVG(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	// Old exports use entities the decoder doesn't know. Truncated or
	// unbalanced files are still refused.
	d.Strict = false
	root := ""
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrCorrupt
		}
		if se, ok := tok.(xml.StartElement); ok && root == "" {
			root = se.Name.Local
		}
	}
	if root != "svg" {
		return ErrCorrupt
	}
	return nil
}
