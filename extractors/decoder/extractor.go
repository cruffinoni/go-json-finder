// Package decoder provides a high-performance streaming JSON extractor.
// It scans only top-level object members, returning the string value of a
// configurable key without allocating the full document.
// Construct an extractor with [NewExtractor].
package decoder

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cruffinoni/go-json-finder/extractor"
)

// Extractor is a streaming JSON extractor configured for a specific top-level key.
// Construct with NewExtractor; a zero-value Extractor is not valid.
type Extractor struct {
	key string
}

// NewExtractor returns an Extractor that extracts the string value of the
// given top-level JSON key. key must be a non-empty, plain ASCII string
// (no escape sequences). It returns an error if key is empty.
func NewExtractor(key string) (Extractor, error) {
	if key == "" {
		return Extractor{}, errors.New("decoder: NewExtractor called with empty key")
	}
	return Extractor{key: key}, nil
}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "decoder"
}

const (
	readerBufferSize     = 64 * 1024 // 64 KiB
	shortStringScanLimit = 256
	whitespaceScanLimit  = 64
	initialReadChunkCap  = 8 * 1024  // 8 KiB
	lateReadChunkCap     = 64 * 1024 // 64 KiB

	hexValueSize           = 4
	lowSurrogateEscapeSize = 6 // `\u` + 4 hex digits

	// wordOnes is a 64-bit mask with every byte set to 0x01.
	// It is used to broadcast a constant to all 8 byte lanes in a word.
	wordOnes uint64 = 0x0101010101010101

	// wordHighs is a 64-bit mask with the high bit of every byte set (0x80).
	// ANDing with this mask isolates the sign bit of each byte lane, which signals
	// an underflow in the SWAR control-byte test.
	wordHighs uint64 = 0x8080808080808080

	// wordThreshold20 is wordOnes * 0x20, i.e. every byte lane set to 0x20 (space).
	// Subtracting this from a word underflows any byte strictly less than 0x20,
	// setting its high bit — detectable via wordHighs.
	wordThreshold20 = wordOnes * 0x20
)

var readerPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(bytes.NewReader(nil), readerBufferSize)
	},
}

var cappedReaderPool = sync.Pool{
	New: func() any {
		return &cappedReader{}
	},
}

// scanner owns the stateful JSON token scan over a buffered reader.
// It validates enough JSON structure to avoid false positives while keeping
// allocation and buffering minimal.
type scanner struct {
	r   *bufio.Reader
	src *cappedReader
}

func (s *scanner) isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func (s *scanner) isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'f') ||
		(b >= 'A' && b <= 'F')
}

func (s *scanner) isNumberStart(b byte) bool {
	return b == '-' || s.isDigit(b)
}

func (s *scanner) invalidJSONf(format string, args ...any) error {
	return fmt.Errorf("invalid json: "+format, args...)
}

func (s *scanner) promoteReadChunkCap(maxChunk int) {
	if s.src == nil {
		return
	}
	s.src.setMaxReadChunk(maxChunk)
}

// peekBufferedChunk returns up to limit buffered bytes without consuming them.
// It ensures at least one byte is readable so callers can treat EOF
// consistently and then chooses between full buffered size or a caller cap.
func (s *scanner) peekBufferedChunk(limit int) ([]byte, error) {
	if s.r.Buffered() == 0 {
		if _, err := s.r.Peek(1); err != nil {
			return nil, err
		}
	}
	chunkSize := s.r.Buffered()
	if limit > 0 && chunkSize > limit {
		chunkSize = limit
	}
	return s.r.Peek(chunkSize)
}

// cappedReader limits each underlying Read call to maxReadChunk bytes.
// It keeps bufio fill batches bounded without imposing a total read limit.
type cappedReader struct {
	base         io.Reader
	maxReadChunk int
}

func (r *cappedReader) reset(base io.Reader, maxReadChunk int) {
	r.base = base
	r.maxReadChunk = maxReadChunk
}

func (r *cappedReader) setMaxReadChunk(maxReadChunk int) {
	if maxReadChunk <= 0 {
		return
	}
	r.maxReadChunk = maxReadChunk
}

// Read implements io.Reader, capping each call to maxReadChunk bytes.
func (r *cappedReader) Read(p []byte) (int, error) {
	if r.base == nil {
		return 0, io.EOF
	}
	if r.maxReadChunk > 0 && len(p) > r.maxReadChunk {
		p = p[:r.maxReadChunk]
	}
	return r.base.Read(p)
}

// controlByteIndex returns the index of the first byte in segment whose value
// is strictly less than 0x20 (a JSON-invalid control character), or -1 if none
// is found.
//
// The implementation uses SWAR (SIMD Within A Register): it processes 8 bytes
// at a time by loading them as a uint64 and applying the bitmask test:
//
//	(word - threshold20) & ^word & wordHighs
//
// For each byte lane, subtracting 0x20 underflows (wraps) if and only if the
// byte is < 0x20. The underflow sets the high bit of that lane; ANDing with
// ^word clears false positives from bytes >= 0x80, and ANDing with wordHighs
// isolates the high bit of each lane. A non-zero result means at least one
// lane contains a control byte.
//
// The outer loop unrolls four 8-byte words (32 bytes) per iteration for
// throughput on long segments. When a batch tests positive, a scalar fallback
// pinpoints the exact byte within the batch.
func (s *scanner) controlByteIndex(segment []byte) int {
	const (
		wordWidth  = 8
		wordBatch4 = 4 * wordWidth // 32 bytes per unrolled iteration
	)

	n := len(segment)
	i := 0

	for ; i+wordBatch4 <= n; i += wordBatch4 {
		u0 := binary.NativeEndian.Uint64(segment[i:])
		u1 := binary.NativeEndian.Uint64(segment[i+8:])
		u2 := binary.NativeEndian.Uint64(segment[i+16:])
		u3 := binary.NativeEndian.Uint64(segment[i+24:])

		if ((u0-wordThreshold20)&(^u0)&wordHighs) == 0 &&
			((u1-wordThreshold20)&(^u1)&wordHighs) == 0 &&
			((u2-wordThreshold20)&(^u2)&wordHighs) == 0 &&
			((u3-wordThreshold20)&(^u3)&wordHighs) == 0 {
			continue
		}
		for j := 0; j < wordBatch4; j++ {
			if segment[i+j] < 0x20 {
				return i + j
			}
		}
	}

	for ; i+wordWidth <= n; i += wordWidth {
		x := binary.NativeEndian.Uint64(segment[i:])
		if ((x - wordThreshold20) & (^x) & wordHighs) == 0 {
			continue
		}
		for j := 0; j < wordWidth; j++ {
			if segment[i+j] < 0x20 {
				return i + j
			}
		}
	}

	for ; i < n; i++ {
		if segment[i] < 0x20 {
			return i
		}
	}

	return -1
}

// findStringStop returns the index and value of the first byte in chunk that
// requires action during string scanning: a closing '"', a '\' escape, or an
// invalid control byte (< 0x20). Returns (-1, 0) when no such byte is found.
//
// For short chunks (≤ shortStringScanLimit) a scalar loop is used directly.
// For larger chunks, bytes.IndexByte locates '"' and '\' first; controlByteIndex
// then scans only the prefix up to the earliest special byte, avoiding a full
// SWAR pass over the whole chunk when a quote or backslash appears early.
func (s *scanner) findStringStop(chunk []byte) (int, byte) {
	if i, b := s.findStringStopSkip(chunk); i >= 0 {
		return i, b
	}
	if ctrlIndex := s.controlByteIndex(chunk); ctrlIndex >= 0 {
		return ctrlIndex, chunk[ctrlIndex]
	}
	return -1, 0
}

// findStringStopSkip is a fast variant of findStringStop for skip-only paths
// (skipString). The correctness trade-off versus findStringStop is intentional:
// when neither '"' nor '\' appears in a large chunk, the entire chunk is safe
// to discard without inspecting for control bytes — those bytes are never
// returned to the caller and cannot corrupt an extracted value. When a '"' or
// '\' is found, controlByteIndex still scans the prefix before it, so control
// bytes that precede a structural marker are not silently accepted.
func (s *scanner) findStringStopSkip(chunk []byte) (int, byte) {
	if len(chunk) <= shortStringScanLimit {
		for i := 0; i < len(chunk); i++ {
			b := chunk[i]
			if b == '"' || b == '\\' || b < 0x20 {
				return i, b
			}
		}
		return -1, 0
	}

	quoteIndex := bytes.IndexByte(chunk, '"')
	slashIndex := bytes.IndexByte(chunk, '\\')

	if quoteIndex == -1 && slashIndex == -1 {
		return -1, 0
	}

	specialIndex, special := quoteIndex, byte('"')
	if slashIndex != -1 && (quoteIndex == -1 || slashIndex < quoteIndex) {
		specialIndex, special = slashIndex, '\\'
	}

	if ctrlIndex := s.controlByteIndex(chunk[:specialIndex]); ctrlIndex >= 0 {
		return ctrlIndex, chunk[ctrlIndex]
	}
	return specialIndex, special
}

// decodeHex4 parses exactly 4 hex digits from content into a uint16 code unit.
// It accepts both upper- and lower-case hex digits (0–9, a–f, A–F) and returns
// an error on any non-hex byte. content must be exactly hexValueSize (4) bytes;
// a shorter slice is treated as an unexpected EOF.
func (s *scanner) decodeHex4(content []byte) (uint16, error) {
	if len(content) != hexValueSize {
		return 0, s.invalidJSONf("unexpected EOF while reading unicode hex16 value")
	}

	var value uint16

	for i := 0; i < hexValueSize; i++ {
		var nibble byte
		switch {
		case content[i] >= '0' && content[i] <= '9':
			nibble = content[i] - '0'
		case content[i] >= 'a' && content[i] <= 'f':
			nibble = content[i] - 'a' + 10
		case content[i] >= 'A' && content[i] <= 'F':
			nibble = content[i] - 'A' + 10
		default:
			return 0, s.invalidJSONf("invalid hex digit %q in unicode escape", content[i])
		}
		value = (value << 4) | uint16(nibble)
	}

	return value, nil
}

// readHex16 reads and decodes one \uXXXX code unit from the stream.
func (s *scanner) readHex16() (uint16, error) {
	var content [hexValueSize]byte
	if _, err := io.ReadFull(s.r, content[:]); err != nil {
		return 0, s.invalidJSONf("unexpected EOF while reading unicode hex16 value")
	}

	value, err := s.decodeHex4(content[:])
	if err != nil {
		return 0, err
	}
	return value, nil
}

// readLowSurrogateAfterHigh validates and consumes the \uXXXX escape that must
// immediately follow a high surrogate (U+D800–U+DBFF) already read from the
// stream. JSON RFC 8259 §7 requires that a high surrogate be paired with a low
// surrogate (U+DC00–U+DFFF) encoded as a second \uXXXX sequence. This method
// peeks ahead to confirm the pattern "\uXXXX" is present before consuming it,
// so the stream position is only advanced on success.
func (s *scanner) readLowSurrogateAfterHigh() (uint16, error) {
	content, err := s.peekBufferedChunk(lowSurrogateEscapeSize)
	if err != nil {
		return 0, s.invalidJSONf("unexpected EOF after high surrogate")
	}
	if len(content) == 0 {
		return 0, s.invalidJSONf("unexpected EOF after high surrogate")
	}
	if content[0] != '\\' {
		return 0, s.invalidJSONf("expected low surrogate after high surrogate")
	}
	if len(content) < 2 {
		return 0, s.invalidJSONf("unexpected EOF after high surrogate")
	}
	if content[1] != 'u' {
		return 0, s.invalidJSONf("expected low surrogate unicode escape")
	}
	if len(content) < lowSurrogateEscapeSize {
		return 0, s.invalidJSONf("unexpected EOF after high surrogate")
	}

	second, err := s.decodeHex4(content[2:])
	if err != nil {
		return 0, err
	}
	if _, err := s.r.Discard(lowSurrogateEscapeSize); err != nil {
		return 0, s.invalidJSONf("unexpected EOF after high surrogate")
	}
	if second < 0xDC00 || second > 0xDFFF {
		return 0, s.invalidJSONf("invalid low surrogate in unicode escape")
	}
	return second, nil
}

// readUnicodeEscape decodes one JSON \uXXXX escape sequence. It handles all
// three cases defined by RFC 8259 §7:
//   - BMP scalar (U+0000–U+D7FF, U+E000–U+FFFF): returned directly as a rune.
//   - High surrogate (U+D800–U+DBFF): must be followed by a low surrogate
//     \uXXXX; both code units are consumed and decoded as a UTF-16 surrogate
//     pair into a single Unicode scalar above U+FFFF.
//   - Lone low surrogate (U+DC00–U+DFFF): invalid at this position; an error
//     is returned without consuming further input.
func (s *scanner) readUnicodeEscape() (rune, error) {
	// JSON \uXXXX always starts with one 16-bit code unit.
	first, err := s.readHex16()
	if err != nil {
		return 0, err
	}

	// A low surrogate cannot appear first; it must follow a high surrogate.
	if first >= 0xDC00 && first <= 0xDFFF {
		return 0, s.invalidJSONf("unexpected low surrogate in unicode escape")
	}

	// Non-high-surrogate values are standalone BMP runes.
	if first < 0xD800 || first > 0xDBFF {
		return rune(first), nil
	}

	// High surrogate must be followed by the complete low-surrogate escape.
	second, err := s.readLowSurrogateAfterHigh()
	if err != nil {
		return 0, err
	}

	// Combine UTF-16 surrogate pair into one Unicode scalar value.
	r := utf16.DecodeRune(rune(first), rune(second))
	if r == utf8.RuneError {
		return 0, s.invalidJSONf("invalid surrogate pair in unicode escape")
	}
	return r, nil
}

// readEscapedRune decodes one JSON escape sequence after '\' has been consumed.
func (s *scanner) readEscapedRune() (rune, error) {
	esc, err := s.r.ReadByte()
	if err != nil {
		return 0, s.invalidJSONf("unexpected EOF in escape sequence")
	}

	switch esc {
	case '"', '\\', '/':
		return rune(esc), nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'u':
		return s.readUnicodeEscape()
	default:
		return 0, s.invalidJSONf("invalid escape sequence \\%c", esc)
	}
}

// readString reads a full JSON string body after the opening quote was consumed.
// It appends plain segments directly from buffered chunks and only enters escape
// decoding when the next special byte is a backslash.
func (s *scanner) readString() (string, error) {
	buf := make([]byte, 0, 16)

	for {
		chunk, err := s.peekBufferedChunk(shortStringScanLimit)
		if err != nil {
			return "", s.invalidJSONf("unexpected EOF while reading string")
		}

		index, special := s.findStringStop(chunk)
		segment := chunk
		consumed := len(chunk)
		if index >= 0 {
			segment = chunk[:index]
			consumed = index + 1
		}
		buf = append(buf, segment...)

		if _, err := s.r.Discard(consumed); err != nil {
			return "", err
		}
		if index < 0 {
			continue
		}
		switch special {
		case '"':
			return string(buf), nil
		case '\\':
			r, err := s.readEscapedRune()
			if err != nil {
				return "", err
			}
			var tmp [utf8.UTFMax]byte
			n := utf8.EncodeRune(tmp[:], r)
			buf = append(buf, tmp[:n]...)
		default:
			return "", s.invalidJSONf("invalid control character in string")
		}
	}
}

// readKeyEquals scans a JSON object key and compares it to target without
// allocating the full key string. Comparison stays in a byte-oriented fast
// path and falls back to mismatch as soon as a non-ASCII escaped rune appears.
func (s *scanner) readKeyEquals(target string) (bool, error) {
	matched := true
	index := 0
	targetLen := len(target)

	for {
		chunk, err := s.peekBufferedChunk(shortStringScanLimit)
		if err != nil {
			return false, s.invalidJSONf("unexpected EOF while reading string")
		}

		specialIndex, special := s.findStringStop(chunk)
		segment := chunk
		consumed := len(chunk)
		if specialIndex >= 0 {
			segment = chunk[:specialIndex]
			consumed = specialIndex + 1
		}

		if matched {
			for _, b := range segment {
				if b > 0x7f || index >= targetLen || target[index] != b {
					matched = false
					break
				}
				index++
			}
		}

		if _, err := s.r.Discard(consumed); err != nil {
			return false, err
		}

		if specialIndex < 0 {
			continue
		}

		if special == '"' {
			return matched && index == targetLen, nil
		}
		if special != '\\' {
			return false, s.invalidJSONf("invalid control character in string")
		}

		r, err := s.readEscapedRune()
		if err != nil {
			return false, err
		}

		if !matched {
			continue
		}
		if r > 0x7f || index >= targetLen || target[index] != byte(r) {
			matched = false
			continue
		}
		index++
	}
}

// parseChannelValue reads the target value when it is a string, or skips the
// value and returns ErrChannelInvalidType for non-string values.
func (s *scanner) parseChannelValue(start byte) (string, error) {
	if start == '"' {
		channel, err := s.readString()
		if err != nil {
			return "", err
		}
		return channel, nil
	}

	if err := s.skipValueFromStart(start); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%w: got non-string value", extractor.ErrChannelInvalidType)
}

// skipEscape validates one JSON escape sequence while skipping string content.
// Unlike readEscapedRune, it does not build runes and only checks shape.
func (s *scanner) skipEscape() error {
	esc, err := s.r.ReadByte()
	if err != nil {
		return s.invalidJSONf("unexpected EOF in escape sequence")
	}

	switch esc {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return nil
	case 'u':
		var hex [4]byte
		if _, err := io.ReadFull(s.r, hex[:]); err != nil {
			return s.invalidJSONf("unexpected EOF in unicode escape")
		}
		for _, b := range hex {
			if !s.isHexDigit(b) {
				return s.invalidJSONf("invalid hex digit %q in unicode escape", b)
			}
		}
		return nil
	default:
		return s.invalidJSONf("invalid escape sequence \\%c", esc)
	}
}

// skipString skips a full JSON string body after the opening quote was consumed.
func (s *scanner) skipString() error {
	for {
		chunk, err := s.peekBufferedChunk(0)
		if err != nil {
			return s.invalidJSONf("unexpected EOF while reading string")
		}

		index, special := s.findStringStopSkip(chunk)
		if index < 0 {
			if _, err := s.r.Discard(len(chunk)); err != nil {
				return err
			}
			continue
		}
		if _, err := s.r.Discard(index + 1); err != nil {
			return err
		}
		if special == '"' {
			return nil
		}
		if special == '\\' {
			if err := s.skipEscape(); err != nil {
				return err
			}
			continue
		}
		return s.invalidJSONf("invalid control character in string")
	}
}

// isValueDelimiter reports whether b can terminate a primitive JSON value.
func (s *scanner) isValueDelimiter(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t', ',', '}', ']':
		return true
	default:
		return false
	}
}

// skipLiteralRemainder validates and consumes the remaining bytes of a JSON
// literal (for true/false/null) after the first byte was already consumed.
func (s *scanner) skipLiteralRemainder(rem string) error {
	var got [4]byte
	if _, err := io.ReadFull(s.r, got[:len(rem)]); err != nil {
		return s.invalidJSONf("unexpected EOF while reading literal")
	}
	for i := 0; i < len(rem); i++ {
		if got[i] != rem[i] {
			return s.invalidJSONf("invalid literal")
		}
	}
	return nil
}

// skipNumberFromStart validates and skips a JSON number where start is already
// consumed as the first byte of the number.
// The helper enforces integer, fraction, and exponent grammar while keeping
// the first non-number delimiter unread for the caller.
// TODO(perf): evaluate a skip-only fast path that scans numeric-token bytes
// until a delimiter instead of fully validating JSON number grammar.
func (s *scanner) skipNumberFromStart(start byte) error {
	readNext := func() (byte, bool, error) {
		b, err := s.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, true, nil
			}
			return 0, false, err
		}
		if s.isValueDelimiter(b) {
			if err := s.r.UnreadByte(); err != nil {
				return 0, false, err
			}
			return 0, true, nil
		}
		return b, false, nil
	}

	first := start
	if first == '-' {
		var delim bool
		var err error
		first, delim, err = readNext()
		if err != nil {
			return err
		}
		if delim {
			return s.invalidJSONf("invalid number")
		}
	}

	if !s.isDigit(first) {
		return s.invalidJSONf("invalid number")
	}

	leadingZero := first == '0'
	for {
		b, delim, err := readNext()
		if err != nil {
			return err
		}
		if delim {
			return nil
		}
		if s.isDigit(b) {
			if leadingZero {
				return s.invalidJSONf("invalid number with leading zero")
			}
			continue
		}

		// Fraction requires at least one digit after the dot.
		if b == '.' {
			b, delim, err = readNext()
			if err != nil {
				return err
			}
			if delim || !s.isDigit(b) {
				return s.invalidJSONf("invalid number fraction")
			}
			for {
				b, delim, err = readNext()
				if err != nil {
					return err
				}
				if delim {
					return nil
				}
				if !s.isDigit(b) {
					break
				}
			}
		}

		// Exponent supports optional sign and at least one digit.
		if b == 'e' || b == 'E' {
			b, delim, err = readNext()
			if err != nil {
				return err
			}
			if delim {
				return s.invalidJSONf("invalid number exponent")
			}
			if b == '+' || b == '-' {
				b, delim, err = readNext()
				if err != nil {
					return err
				}
				if delim {
					return s.invalidJSONf("invalid number exponent")
				}
			}
			if !s.isDigit(b) {
				return s.invalidJSONf("invalid number exponent")
			}
			for {
				b, delim, err = readNext()
				if err != nil {
					return err
				}
				if delim {
					return nil
				}
				if !s.isDigit(b) {
					return s.invalidJSONf("invalid character %q in number", b)
				}
			}
		}

		return s.invalidJSONf("invalid character %q in number", b)
	}
}

// skipComposite skips a complete JSON object or array, starting after the
// opening '{' or '[' has already been consumed (depth begins at 1).
//
// The key invariant is that structural bytes ('{', '}', '[', ']') inside string
// values must not affect the depth counter. To enforce this, the scanner
// maintains an inString flag: while inside a string it delegates to
// findStringStop to advance past plain text and detect '"' (end of string) or
// '\' (escape sequence to validate). When an escape is found, skipEscape
// consumes and validates the escape unit, then the outer loop restarts from a
// fresh buffered chunk (via continue outer) because the chunk slice is now
// stale. Outside strings, only '{', '[', '}', ']', and '"' are relevant.
func (s *scanner) skipComposite() error {
	depth := 1
	inString := false

	for {
		chunk, err := s.peekBufferedChunk(0)
		if err != nil {
			return s.invalidJSONf("unexpected EOF while skipping composite value")
		}

		escaped := false
		for i := 0; i < len(chunk); i++ {
			b := chunk[i]
			if inString {
				// In string mode we only look for quote/escape markers. Any
				// structural characters are plain text and must be ignored.
				index, special := s.findStringStop(chunk[i:])
				if index < 0 {
					break
				}
				i += index

				if special == '"' {
					inString = false
					continue
				}
				if special < 0x20 {
					return s.invalidJSONf("invalid control character in string")
				}
				// Consume up to '\' then validate the escape unit. The chunk is
				// now stale so break and let the outer loop re-peek.
				if _, err := s.r.Discard(i + 1); err != nil {
					return err
				}
				if err := s.skipEscape(); err != nil {
					return err
				}
				escaped = true
				break
			}

			switch b {
			case '"':
				inString = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					if _, err := s.r.Discard(i + 1); err != nil {
						return err
					}
					return nil
				}
			}
		}

		if !escaped {
			if _, err := s.r.Discard(len(chunk)); err != nil {
				return err
			}
		}
	}
}

// skipValueFromStart skips exactly one JSON value whose first byte is already
// consumed in start.
func (s *scanner) skipValueFromStart(start byte) error {
	switch start {
	case '"':
		s.promoteReadChunkCap(lateReadChunkCap)
		return s.skipString()
	case '{', '[':
		s.promoteReadChunkCap(lateReadChunkCap)
		return s.skipComposite()
	case 't':
		return s.skipLiteralRemainder("rue")
	case 'f':
		return s.skipLiteralRemainder("alse")
	case 'n':
		return s.skipLiteralRemainder("ull")
	default:
		if s.isNumberStart(start) {
			return s.skipNumberFromStart(start)
		}
		return s.invalidJSONf("unexpected value start %q", start)
	}
}

// readNonSpace returns the next non-whitespace byte and consumes it.
// Whitespace is skipped in buffered chunks for fewer reader calls.
func (s *scanner) readNonSpace() (byte, error) {
	for {
		chunk, err := s.peekBufferedChunk(whitespaceScanLimit)
		if err != nil {
			return 0, err
		}
		// Find first non-space
		i := 0
		for i < len(chunk) {
			switch chunk[i] {
			case ' ', '\n', '\r', '\t':
				i++
				continue
			default:
				// Discard up to and including this byte
				b := chunk[i]
				_, _ = s.r.Discard(i + 1)
				return b, nil
			}
		}
		// Whole chunk is whitespace
		_, _ = s.r.Discard(len(chunk))
	}
}

// ensureDocumentEnd verifies that only trailing JSON whitespace remains.
func (s *scanner) ensureDocumentEnd() error {
	for {
		b, err := s.readNonSpace()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		return s.invalidJSONf("unexpected trailing data after top-level object: got %q", b)
	}
}

// Extract scans a top-level object and returns the string value of the
// configured key. It exits early on the first match and validates trailing
// non-space bytes only when the object ends without a matching target key.
func (e Extractor) Extract(r io.Reader) (string, error) {
	if e.key == "" {
		return "", errors.New("decoder: Extract called on zero-value Extractor; use NewExtractor")
	}
	br := readerPool.Get().(*bufio.Reader)
	src := cappedReaderPool.Get().(*cappedReader)
	src.reset(r, initialReadChunkCap)
	br.Reset(src)
	defer func() {
		src.reset(nil, 0)
		cappedReaderPool.Put(src)
		br.Reset(bytes.NewReader(nil))
		readerPool.Put(br)
	}()
	s := scanner{r: br, src: src}

	start, err := s.readNonSpace()
	if err != nil {
		return "", fmt.Errorf("read first byte: %w", err)
	}
	if start != '{' {
		return "", fmt.Errorf("expected '{' at beginning JSON, got %q", start)
	}

	for {
		start, err = s.readNonSpace()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", fmt.Errorf("scan root object: %w", s.invalidJSONf("unexpected EOF while reading object"))
			}
			return "", fmt.Errorf("scan root object: %w", err)
		}

		if start == '}' {
			// For the not-found path we require strict end-of-document.
			if err := s.ensureDocumentEnd(); err != nil {
				return "", fmt.Errorf("scan root object: %w", err)
			}
			return "", extractor.ErrChannelNotFound
		}
		if start != '"' {
			return "", fmt.Errorf("scan root object: %w", s.invalidJSONf("expected object key, got %q", start))
		}

		keyIsChannel, err := s.readKeyEquals(e.key)
		if err != nil {
			return "", fmt.Errorf("scan root object key: %w", err)
		}

		sep, err := s.readNonSpace()
		if err != nil {
			return "", fmt.Errorf("scan root object separator: %w", s.invalidJSONf("unexpected EOF after object key"))
		}
		if sep != ':' {
			return "", fmt.Errorf("scan root object separator: %w", s.invalidJSONf("expected ':' after object key, got %q", sep))
		}

		valueStart, err := s.readNonSpace()
		if err != nil {
			return "", fmt.Errorf("scan root object value: %w", s.invalidJSONf("unexpected EOF while reading object value"))
		}

		if keyIsChannel {
			value, err := s.parseChannelValue(valueStart)
			if err != nil {
				return "", fmt.Errorf("scan channel value: %w", err)
			}
			return value, nil
		}

		if err := s.skipValueFromStart(valueStart); err != nil {
			return "", fmt.Errorf("skip non-channel value: %w", err)
		}

		next, err := s.readNonSpace()
		if err != nil {
			return "", fmt.Errorf("scan root object delimiter: %w", s.invalidJSONf("unexpected EOF after object value"))
		}
		if next == '}' {
			if err := s.ensureDocumentEnd(); err != nil {
				return "", fmt.Errorf("scan root object: %w", err)
			}
			return "", extractor.ErrChannelNotFound
		}
		if next != ',' {
			return "", fmt.Errorf("scan root object delimiter: %w", s.invalidJSONf("expected ',' or '}' after object value, got %q", next))
		}
	}
}
