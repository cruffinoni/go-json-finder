// Package decoder provides a streaming extractor that scans root object
// members and returns the first top-level "channel" key value.
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

// Extractor implements extractor.Extractor with a streaming scanner that
// stops as soon as the first top-level "channel" value is extracted.
type Extractor struct{}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "decoder"
}

const (
	channelKey           = "channel"
	readerBufferSize     = 4 * 1024 // 4 KiB
	shortStringScanLimit = 256

	hexValueSize           = 4
	lowSurrogateEscapeSize = 6 // `\u` + 4 hex digits
)

var readerPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(bytes.NewReader(nil), readerBufferSize)
	},
}

// scanner owns the stateful JSON token scan over a buffered reader.
// It validates enough JSON structure to avoid false positives while keeping
// allocation and buffering minimal.
type scanner struct {
	r *bufio.Reader
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

// peekBufferedChunk returns up to limit buffered bytes without consuming them.
// It first ensures at least one byte is readable so callers can treat EOF
// consistently and then choose between full buffered size or a caller cap.
func (s *scanner) peekBufferedChunk(limit int) ([]byte, error) {
	if _, err := s.r.Peek(1); err != nil {
		return nil, err
	}

	chunkSize := s.r.Buffered()
	if limit > 0 && chunkSize > limit {
		chunkSize = limit
	}

	chunk, err := s.r.Peek(chunkSize)
	if err != nil {
		return nil, err
	}
	return chunk, nil
}

// validateStringSegment ensures a raw JSON string fragment does not contain
// unescaped control characters (bytes < 0x20), which are invalid in JSON.
//
// The fast path checks bytes in 32-byte batches (4x uint64 lanes) using the
// classic hasLess bit trick: ((x-threshold) & ^x & highs) sets a lane high bit
// iff a byte is < 0x20. We can return immediately on the first match.
// Remaining tail bytes (<8) are checked with a simple loop.
func (s *scanner) validateStringSegment(segment []byte) error {
	const (
		ones  uint64 = 0x0101010101010101
		highs uint64 = 0x8080808080808080
		// 0x20 repeated in each byte lane.
		threshold = ones * 0x20
	)

	n := len(segment)
	i := 0
	for ; i+32 <= n; i += 32 {
		x := binary.LittleEndian.Uint64(segment[i:]) | binary.LittleEndian.Uint64(segment[i+8:]) | binary.LittleEndian.Uint64(segment[i+16:]) | binary.LittleEndian.Uint64(segment[i+24:])

		if ((x - threshold) & (^x) & highs) != 0 {
			return s.invalidJSONf("invalid control character in string")
		}
	}

	for ; i+8 <= n; i += 8 {
		x := binary.LittleEndian.Uint64(segment[i:])
		if ((x - threshold) & (^x) & highs) != 0 {
			return s.invalidJSONf("invalid control character in string")
		}
	}

	for ; i < n; i++ {
		if segment[i] < 0x20 {
			return s.invalidJSONf("invalid control character in string")
		}
	}
	return nil
}

// stringSpecialIndex returns the earliest quote or backslash position in chunk.
// The returned byte identifies which marker was found.
func (s *scanner) stringSpecialIndex(chunk []byte) (int, byte) {
	quoteIndex := bytes.IndexByte(chunk, '"')
	slashIndex := bytes.IndexByte(chunk, '\\')

	switch {
	case quoteIndex == -1 && slashIndex == -1:
		return -1, 0
	case quoteIndex == -1:
		return slashIndex, '\\'
	case slashIndex == -1:
		return quoteIndex, '"'
	case quoteIndex < slashIndex:
		return quoteIndex, '"'
	default:
		return slashIndex, '\\'
	}
}

// decodeHex4 parses a 4-hex-digit JSON unicode unit from content.
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

// readLowSurrogateAfterHigh validates and consumes the low surrogate escape
// that must follow an already read high surrogate.
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

// readUnicodeEscape decodes one JSON unicode escape sequence, including
// surrogate pair handling when needed.
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

		index, special := s.stringSpecialIndex(chunk)
		segment := chunk
		consumed := len(chunk)
		if index >= 0 {
			segment = chunk[:index]
			consumed = index + 1
		}
		if err := s.validateStringSegment(segment); err != nil {
			return "", err
		}
		buf = append(buf, segment...)

		if _, err := s.r.Discard(consumed); err != nil {
			return "", err
		}
		if index < 0 {
			continue
		}
		if special < 0x20 {
			return "", s.invalidJSONf("invalid control character in string")
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
			return "", s.invalidJSONf("unexpected byte %q while reading string", special)
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

		specialIndex, special := s.stringSpecialIndex(chunk)
		segment := chunk
		consumed := len(chunk)
		if specialIndex >= 0 {
			segment = chunk[:specialIndex]
			consumed = specialIndex + 1
		}
		if err := s.validateStringSegment(segment); err != nil {
			return false, err
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
		if special < 0x20 {
			return false, s.invalidJSONf("invalid control character in string")
		}

		if special == '"' {
			return matched && index == targetLen, nil
		}
		if special != '\\' {
			return false, s.invalidJSONf("unexpected byte %q while reading string", special)
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

// parseChannelValue reads "channel" value when it is a string, or skips the
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

		index, special := s.stringSpecialIndex(chunk)
		segment := chunk
		consumed := len(chunk)
		if index >= 0 {
			segment = chunk[:index]
			consumed = index + 1
		}
		if err := s.validateStringSegment(segment); err != nil {
			return err
		}
		if _, err := s.r.Discard(consumed); err != nil {
			return err
		}
		if index < 0 {
			continue
		}
		if special < 0x20 {
			return s.invalidJSONf("invalid control character in string")
		}
		if special == '"' {
			return nil
		}
		if err := s.skipEscape(); err != nil {
			return err
		}
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

// skipComposite skips a full object or array value, tracking nesting and
// string/escape state so structural bytes inside strings are ignored.
func (s *scanner) skipComposite() error {
	depth := 1
	inString := false

outer:
	for {
		chunk, err := s.peekBufferedChunk(0)
		if err != nil {
			return s.invalidJSONf("unexpected EOF while skipping composite value")
		}

		for i := 0; i < len(chunk); i++ {
			b := chunk[i]
			if inString {
				// In string mode we only look for quote/escape markers. Any
				// structural characters are plain text and must be ignored.
				index, special := s.stringSpecialIndex(chunk[i:])
				segment := chunk[i:]
				if index >= 0 {
					segment = segment[:index]
				}
				if err := s.validateStringSegment(segment); err != nil {
					return err
				}
				i += len(segment)
				if index < 0 {
					break
				}
				if special < 0x20 {
					return s.invalidJSONf("invalid control character in string")
				}

				if special == '"' {
					inString = false
					continue
				}
				// For escapes, consume up to '\' then validate the escaped unit
				// via skipEscape, and restart from a fresh buffered chunk.
				if _, err := s.r.Discard(i + 1); err != nil {
					return err
				}
				if err := s.skipEscape(); err != nil {
					return err
				}
				continue outer
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

		if _, err := s.r.Discard(len(chunk)); err != nil {
			return err
		}
	}
}

// skipValueFromStart skips exactly one JSON value whose first byte is already
// consumed in start.
func (s *scanner) skipValueFromStart(start byte) error {
	switch start {
	case '"':
		return s.skipString()
	case '{', '[':
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
		chunk, err := s.peekBufferedChunk(0)
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

// Extract scans a top-level object and returns the root "channel" string value.
// It exits early on the first top-level "channel" key and validates trailing
// non-space bytes only when the object ends without a matching channel.
func (Extractor) Extract(r io.Reader) (string, error) {
	br := readerPool.Get().(*bufio.Reader)
	br.Reset(r)
	defer func() {
		br.Reset(bytes.NewReader(nil))
		readerPool.Put(br)
	}()
	s := scanner{r: br}

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

		keyIsChannel, err := s.readKeyEquals(channelKey)
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
