// Package extractor defines the shared contract and sentinel errors used by
// all channel extraction strategies in this repository.
package extractor

import (
	"errors"
	"io"
)

// Extractor defines a channel extraction strategy.
//
// Implementations intentionally differ in scope:
// - decoder scans tokens and can find nested "channel" keys.
// - structs decodes only top-level "channel" into a struct field.
type Extractor interface {
	Name() string
	Extract(r io.Reader) (string, error)
}

var (
	// ErrChannelNotFound indicates that no matching "channel" key was found.
	ErrChannelNotFound = errors.New("channel not found")
	// ErrChannelInvalidType indicates that "channel" exists but is not a string.
	ErrChannelInvalidType = errors.New("channel must be a JSON string")
)
