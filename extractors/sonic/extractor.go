// Package sonic provides a top-level "channel" extractor based on
// github.com/bytedance/sonic.
package sonic

import (
	"fmt"
	"io"

	sonicjson "github.com/bytedance/sonic"
	sonicast "github.com/bytedance/sonic/ast"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

func typeName(t int) string {
	switch t {
	case sonicast.V_NULL:
		return "null"
	case sonicast.V_TRUE, sonicast.V_FALSE:
		return "boolean"
	case sonicast.V_NUMBER:
		return "number"
	case sonicast.V_STRING:
		return "string"
	case sonicast.V_OBJECT:
		return "object"
	case sonicast.V_ARRAY:
		return "array"
	default:
		return "unknown"
	}
}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "sonic"
}

// Extract reads the payload and extracts the top-level "channel" key.
func (Extractor) Extract(r io.Reader) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read payload: %w", err)
	}

	channel, err := sonicjson.Get(body, "channel")
	if err != nil {
		if err == sonicast.ErrNotExist {
			return "", extractor.ErrChannelNotFound
		}
		return "", fmt.Errorf("parse payload: %w", err)
	}

	if channel.TypeSafe() != sonicast.V_STRING {
		return "", fmt.Errorf("%w: got %s", extractor.ErrChannelInvalidType, typeName(channel.TypeSafe()))
	}

	value, err := channel.StrictString()
	if err != nil {
		return "", fmt.Errorf("read channel value: %w", err)
	}

	return value, nil
}
