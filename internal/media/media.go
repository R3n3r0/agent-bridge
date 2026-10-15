// Package media turns image references from requests (data: URLs and
// http(s) URLs) into validated image bytes.
package media

import (
	"context"
	"errors"
)

// Supported image types.
var supportedTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// ErrInvalid wraps every error caused by the image itself or its URL, as
// opposed to internal failures.
var ErrInvalid = errors.New("invalid image")

// Image is a validated image.
type Image struct {
	MediaType string
	Data      []byte
}

// Resolver loads the image an URL refers to.
type Resolver interface {
	Resolve(ctx context.Context, url string) (Image, error)
}
