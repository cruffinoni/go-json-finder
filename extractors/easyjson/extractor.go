// Package easyjson provides a top-level "channel" extractor based on
// github.com/mailru/easyjson generated unmarshalling.
package easyjson

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"io"

	mailrueasyjson "github.com/mailru/easyjson"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "easyjson"
}

func typeName(raw []byte) string {
	if len(raw) == 0 {
		return "unknown"
	}

	switch raw[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return "number"
	default:
		return "unknown"
	}
}

// Extract reads the payload and extracts the top-level "channel" key.
func (Extractor) Extract(r io.Reader) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read payload: %w", err)
	}

	var p payload
	if err := mailrueasyjson.Unmarshal(body, &p); err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}

	raw := bytes.TrimSpace(p.Channel)
	if len(raw) == 0 {
		return "", extractor.ErrChannelNotFound
	}

	if raw[0] != '"' {
		return "", fmt.Errorf("%w: got %s", extractor.ErrChannelInvalidType, typeName(raw))
	}

	var channel string
	if err := stdjson.Unmarshal(raw, &channel); err != nil {
		return "", fmt.Errorf("decode channel value: %w", err)
	}

	return channel, nil
}
