// Package gojson provides a top-level "channel" extractor based on
// github.com/goccy/go-json.
package gojson

import (
	"errors"
	"fmt"
	"io"
	"strings"

	goccyjson "github.com/goccy/go-json"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "go-json"
}

type payload struct {
	Channel *string `json:"channel"`
}

// Extract reads the payload and extracts the top-level "channel" key.
func (Extractor) Extract(r io.Reader) (string, error) {
	var p payload
	if err := goccyjson.NewDecoder(r).Decode(&p); err != nil {
		if typeErr, ok := errors.AsType[*goccyjson.UnmarshalTypeError](err); ok {
			field := strings.TrimPrefix(typeErr.Field, "payload.")
			if strings.EqualFold(field, "channel") {
				return "", fmt.Errorf("%w: got %s", extractor.ErrChannelInvalidType, typeErr.Value)
			}
		}
		return "", fmt.Errorf("decode payload: %w", err)
	}

	if p.Channel == nil {
		return "", extractor.ErrChannelNotFound
	}

	return *p.Channel, nil
}
