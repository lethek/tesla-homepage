package icons

import (
	"encoding/base64"
	"errors"
	"strings"
)

// IsDataURL reports whether raw is a data: URL.
func IsDataURL(raw string) bool {
	return len(raw) >= 5 && strings.EqualFold(raw[:5], "data:")
}

// decodeDataURL returns the bytes inside a base64 data:image/... URL. Only
// the size and the encoding are checked here. What the bytes are is decided
// afterwards by sniffing them, exactly as for an upload, so the declared media
// type is never trusted. Errors never repeat the URL, which can be megabytes
// long and would otherwise reach the console log and the form.
func decodeDataURL(raw string) ([]byte, error) {
	header, payload, ok := strings.Cut(raw[len("data:"):], ",")
	if !ok {
		return nil, errors.New("invalid data URL")
	}
	header = strings.ToLower(header)
	mediaType, ok := strings.CutSuffix(header, ";base64")
	if !ok {
		return nil, errors.New("data URLs must be base64 encoded")
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return nil, errors.New("data URLs must be images (data:image/...)")
	}
	// Checked before decoding, so an oversized paste costs nothing.
	if len(payload) > base64.StdEncoding.EncodedLen(MaxSize) {
		return nil, errors.New("data URL: image is larger than the limit")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, errors.New("data URL: the base64 data is not valid")
	}
	if len(data) > MaxSize {
		return nil, errors.New("data URL: image is larger than the limit")
	}
	return data, nil
}
