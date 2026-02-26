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
	channel := fastjson.GetString(body, "channel")
	if channel == "" {
		return "", extractor.ErrChannelNotFound
	}

	return channel, nil
}
