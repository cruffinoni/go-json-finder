// Package structs provides a simple top-level "channel" extractor
// based on encoding/json unmarshalling into a typed payload.
package structs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "struct"
}

type payload struct {
	Channel *string `json:"channel"`
}

// Extract reads the payload and extracts the top-level "channel" key.
func (Extractor) Extract(r io.Reader) (string, error) {
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && typeErr.Field == "channel" {
			return "", fmt.Errorf("%w: got %s", extractor.ErrChannelInvalidType, typeErr.Value)
		}
		return "", fmt.Errorf("decode payload: %w", err)
	}

	if p.Channel == nil {
		return "", extractor.ErrChannelNotFound
	}

	return *p.Channel, nil
}
