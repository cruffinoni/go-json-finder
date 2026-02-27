// Package fastjson provides a top-level "channel" extractor based on
// github.com/valyala/fastjson.
package fastjson

import (
	"fmt"
	"io"

	"github.com/valyala/fastjson"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

func typeName(t fastjson.Type) string {
	switch t {
	case fastjson.TypeTrue, fastjson.TypeFalse:
		return "boolean"
	default:
		return t.String()
	}
}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "fastjson"
}

// Extract reads the payload and extracts the top-level "channel" key.
func (Extractor) Extract(r io.Reader) (string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read payload: %w", err)
	}

	var p fastjson.Parser
	value, err := p.ParseBytes(body)
	if err != nil {
		return "", fmt.Errorf("parse payload: %w", err)
	}

	channel := value.Get("channel")
	if channel == nil {
		return "", extractor.ErrChannelNotFound
	}

	if channel.Type() != fastjson.TypeString {
		return "", fmt.Errorf("%w: got %s", extractor.ErrChannelInvalidType, typeName(channel.Type()))
	}

	return string(channel.GetStringBytes()), nil
}
