// Package gjson provides a top-level "channel" extractor based on
// github.com/tidwall/gjson.
package gjson

import (
	"fmt"
	"io"

	"github.com/tidwall/gjson"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "gjson"
}

func typeName(t gjson.Type) string {
	switch t {
	case gjson.Null:
		return "null"
	case gjson.False:
		return "boolean"
	case gjson.True:
		return "boolean"
	case gjson.Number:
		return "number"
	case gjson.String:
		return "string"
	case gjson.JSON:
		return "object"
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

	channel := gjson.GetBytes(body, "channel")
	if channel.Exists() {
		if channel.Type != gjson.String {
			return "", fmt.Errorf("%w: got %s", extractor.ErrChannelInvalidType, typeName(channel.Type))
		}
		return channel.String(), nil
	}

	if !gjson.ValidBytes(body) {
		return "", fmt.Errorf("invalid json")
	}

	return "", extractor.ErrChannelNotFound
}
