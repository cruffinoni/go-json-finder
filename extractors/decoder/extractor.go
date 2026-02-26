// Package decoder provides a streaming extractor that returns the
// first structural "channel" key found in JSON document order.
package decoder

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cruffinoni/go-json-finder/extractor"
)

type Extractor struct{}

// Name returns the extractor identifier.
func (Extractor) Name() string {
	return "decoder"
}

const channelKey = "channel"

var readerPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(bytes.NewReader(nil), 4096)
	},
}

type scanner struct {
	r *bufio.Reader
}

const shortStringScanLimit = 256

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isNumberStart(b byte) bool {
	return b == '-' || isDigit(b)
}

func invalidJSONf(format string, args ...any) error {
	return fmt.Errorf("invalid json: "+format, args...)
}

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

func validateStringSegment(segment []byte) error {
	for _, b := range segment {
		if b < 0x20 {
			return invalidJSONf("invalid control character in string")
		}
	}
	return nil
}

func stringSpecialIndex(chunk []byte) (int, byte) {
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

func (s *scanner) readHex16() (uint16, error) {
	var value uint16

	for i := 0; i < 4; i++ {
		b, err := s.r.ReadByte()
		if err != nil {
			return 0, invalidJSONf("unexpected EOF in unicode escape")
		}

		var nibble byte
		switch {
		case b >= '0' && b <= '9':
			nibble = b - '0'
		case b >= 'a' && b <= 'f':
			nibble = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			nibble = b - 'A' + 10
		default:
			return 0, invalidJSONf("invalid hex digit %q in unicode escape", b)
		}
		value = (value << 4) | uint16(nibble)
	}

	return value, nil
}

func (s *scanner) readUnicodeEscape() (rune, error) {
	first, err := s.readHex16()
	if err != nil {
		return 0, err
	}

	if first >= 0xDC00 && first <= 0xDFFF {
		return 0, invalidJSONf("unexpected low surrogate in unicode escape")
	}

	if first < 0xD800 || first > 0xDBFF {
		return rune(first), nil
	}

	slash, err := s.r.ReadByte()
	if err != nil {
		return 0, invalidJSONf("unexpected EOF after high surrogate")
	}
	if slash != '\\' {
		return 0, invalidJSONf("expected low surrogate after high surrogate")
	}

	u, err := s.r.ReadByte()
	if err != nil {
		return 0, invalidJSONf("unexpected EOF after high surrogate")
	}
	if u != 'u' {
		return 0, invalidJSONf("expected low surrogate unicode escape")
	}

	second, err := s.readHex16()
	if err != nil {
		return 0, err
	}
	if second < 0xDC00 || second > 0xDFFF {
		return 0, invalidJSONf("invalid low surrogate in unicode escape")
	}

	r := utf16.DecodeRune(rune(first), rune(second))
	if r == utf8.RuneError {
		return 0, invalidJSONf("invalid surrogate pair in unicode escape")
	}
	return r, nil
}

func (s *scanner) readEscapedRune() (rune, error) {
	esc, err := s.r.ReadByte()
	if err != nil {
		return 0, invalidJSONf("unexpected EOF in escape sequence")
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
		return 0, invalidJSONf("invalid escape sequence \\%c", esc)
	}
}

func (s *scanner) skipString() error {
	for {
		chunk, err := s.peekBufferedChunk(0)
		if err != nil {
			return invalidJSONf("unexpected EOF while reading string")
		}

		index, special := stringSpecialIndex(chunk)
		segment := chunk
		consumed := len(chunk)
		if index >= 0 {
			segment = chunk[:index]
			consumed = index + 1
		}

		if err := validateStringSegment(segment); err != nil {
			return err
		}

		if _, err := s.r.Discard(consumed); err != nil {
			return err
		}
		if index < 0 {
			continue
		}

		switch special {
		case '"':
			return nil
		case '\\':
			if _, err := s.readEscapedRune(); err != nil {
				return err
			}
			continue
		default:
			return invalidJSONf("unexpected byte %q while reading string", special)
		}
	}
}

func (s *scanner) readString() (string, error) {
	buf := make([]byte, 0, 16)

	for {
		chunk, err := s.peekBufferedChunk(shortStringScanLimit)
		if err != nil {
			return "", invalidJSONf("unexpected EOF while reading string")
		}

		index, special := stringSpecialIndex(chunk)
		segment := chunk
		consumed := len(chunk)
		if index >= 0 {
			segment = chunk[:index]
			consumed = index + 1
		}

		if err := validateStringSegment(segment); err != nil {
			return "", err
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
			return "", invalidJSONf("unexpected byte %q while reading string", special)
		}
	}
}

func (s *scanner) readKeyEquals(target string) (bool, error) {
	matched := true
	index := 0

	for {
		chunk, err := s.peekBufferedChunk(shortStringScanLimit)
		if err != nil {
			return false, invalidJSONf("unexpected EOF while reading string")
		}

		specialIndex, special := stringSpecialIndex(chunk)
		segment := chunk
		consumed := len(chunk)
		if specialIndex >= 0 {
			segment = chunk[:specialIndex]
			consumed = specialIndex + 1
		}

		if err := validateStringSegment(segment); err != nil {
			return false, err
		}

		if matched {
			for _, b := range segment {
				if b > 0x7f || index >= len(target) || target[index] != b {
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
			return matched && index == len(target), nil
		}
		if special != '\\' {
			return false, invalidJSONf("unexpected byte %q while reading string", special)
		}

		r, err := s.readEscapedRune()
		if err != nil {
			return false, err
		}

		if !matched {
			continue
		}
		if r > 0x7f || index >= len(target) || target[index] != byte(r) {
			matched = false
			continue
		}
		index++
	}
}

func (s *scanner) consumeLiteral(suffix string) error {
	for i := 0; i < len(suffix); i++ {
		b, err := s.r.ReadByte()
		if err != nil {
			return invalidJSONf("unexpected EOF while reading literal")
		}
		if b != suffix[i] {
			return invalidJSONf("invalid literal")
		}
	}
	return nil
}

func (s *scanner) skipNumber(first byte) error {
	b := first
	if b == '-' {
		next, err := s.r.ReadByte()
		if err != nil {
			return invalidJSONf("unexpected EOF in number")
		}
		b = next
	}

	switch {
	case b == '0':
		next, err := s.r.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if isDigit(next) {
			return invalidJSONf("leading zero in number")
		}
		if err := s.r.UnreadByte(); err != nil {
			return err
		}
	case b >= '1' && b <= '9':
		for {
			next, err := s.r.ReadByte()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if !isDigit(next) {
				if err := s.r.UnreadByte(); err != nil {
					return err
				}
				break
			}
		}
	default:
		return invalidJSONf("invalid number")
	}

	next, err := s.r.ReadByte()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}

	if next == '.' {
		frac, err := s.r.ReadByte()
		if err != nil {
			return invalidJSONf("invalid fraction in number")
		}
		if !isDigit(frac) {
			return invalidJSONf("invalid fraction in number")
		}
		for {
			frac, err = s.r.ReadByte()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if !isDigit(frac) {
				next = frac
				break
			}
		}
	}

	if next == 'e' || next == 'E' {
		exp, err := s.r.ReadByte()
		if err != nil {
			return invalidJSONf("invalid exponent in number")
		}

		if exp == '+' || exp == '-' {
			exp, err = s.r.ReadByte()
			if err != nil {
				return invalidJSONf("invalid exponent in number")
			}
		}

		if !isDigit(exp) {
			return invalidJSONf("invalid exponent in number")
		}

		for {
			exp, err = s.r.ReadByte()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if !isDigit(exp) {
				if err := s.r.UnreadByte(); err != nil {
					return err
				}
				return nil
			}
		}
	}

	if err := s.r.UnreadByte(); err != nil {
		return err
	}
	return nil
}

func (s *scanner) parseChannelValue(start byte) (string, error) {
	switch {
	case start == '"':
		return s.readString()
	case start == '{':
		return "", fmt.Errorf("%w: got object", extractor.ErrChannelInvalidType)
	case start == '[':
		return "", fmt.Errorf("%w: got array", extractor.ErrChannelInvalidType)
	case start == 't':
		if err := s.consumeLiteral("rue"); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: got boolean", extractor.ErrChannelInvalidType)
	case start == 'f':
		if err := s.consumeLiteral("alse"); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: got boolean", extractor.ErrChannelInvalidType)
	case start == 'n':
		if err := s.consumeLiteral("ull"); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: got null", extractor.ErrChannelInvalidType)
	case isNumberStart(start):
		if err := s.skipNumber(start); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: got number", extractor.ErrChannelInvalidType)
	default:
		return "", invalidJSONf("unexpected byte %q for channel value", start)
	}
}

func (s *scanner) parseObject() (string, bool, error) {
	next, err := s.readNonSpace()
	if err != nil {
		return "", false, invalidJSONf("unexpected EOF while reading object")
	}
	if next == '}' {
		return "", false, nil
	}

	for {
		if next != '"' {
			return "", false, invalidJSONf("expected object key, got %q", next)
		}

		keyIsChannel, err := s.readKeyEquals(channelKey)
		if err != nil {
			return "", false, err
		}

		sep, err := s.readNonSpace()
		if err != nil {
			return "", false, invalidJSONf("unexpected EOF after object key")
		}
		if sep != ':' {
			return "", false, invalidJSONf("expected ':' after object key, got %q", sep)
		}

		valueStart, err := s.readNonSpace()
		if err != nil {
			return "", false, invalidJSONf("unexpected EOF while reading object value")
		}

		if keyIsChannel {
			channel, err := s.parseChannelValue(valueStart)
			if err != nil {
				return "", false, err
			}
			return channel, true, nil
		}

		channel, found, err := s.parseValueFromStart(valueStart)
		if err != nil {
			return "", false, err
		}
		if found {
			return channel, true, nil
		}

		next, err = s.readNonSpace()
		if err != nil {
			return "", false, invalidJSONf("unexpected EOF while reading object")
		}
		if next == ',' {
			next, err = s.readNonSpace()
			if err != nil {
				return "", false, invalidJSONf("unexpected EOF while reading object key")
			}
			continue
		}
		if next == '}' {
			return "", false, nil
		}
		return "", false, invalidJSONf("expected ',' or '}', got %q", next)
	}
}

func (s *scanner) parseArray() (string, bool, error) {
	next, err := s.readNonSpace()
	if err != nil {
		return "", false, invalidJSONf("unexpected EOF while reading array")
	}
	if next == ']' {
		return "", false, nil
	}

	for {
		channel, found, err := s.parseValueFromStart(next)
		if err != nil {
			return "", false, err
		}
		if found {
			return channel, true, nil
		}

		next, err = s.readNonSpace()
		if err != nil {
			return "", false, invalidJSONf("unexpected EOF while reading array")
		}
		if next == ',' {
			next, err = s.readNonSpace()
			if err != nil {
				return "", false, invalidJSONf("unexpected EOF while reading array item")
			}
			continue
		}
		if next == ']' {
			return "", false, nil
		}
		return "", false, invalidJSONf("expected ',' or ']', got %q", next)
	}
}

func (s *scanner) parseValueFromStart(start byte) (string, bool, error) {
	switch {
	case start == '{':
		return s.parseObject()
	case start == '[':
		return s.parseArray()
	case start == '"':
		return "", false, s.skipString()
	case start == 't':
		return "", false, s.consumeLiteral("rue")
	case start == 'f':
		return "", false, s.consumeLiteral("alse")
	case start == 'n':
		return "", false, s.consumeLiteral("ull")
	case isNumberStart(start):
		return "", false, s.skipNumber(start)
	default:
		return "", false, invalidJSONf("unexpected byte %q while reading value", start)
	}
}

func (s *scanner) readNonSpace() (byte, error) {
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return b, nil
		}
	}
}

// Extract scans the input stream and returns the first "channel" string value.
func (Extractor) Extract(r io.Reader) (string, error) {
	br := readerPool.Get().(*bufio.Reader)
	br.Reset(r)
	defer func() {
		br.Reset(bytes.NewReader(nil))
		readerPool.Put(br)
	}()

	s := scanner{r: br}

	for {
		start, err := s.readNonSpace()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", extractor.ErrChannelNotFound
			}
			return "", fmt.Errorf("read json token: %w", err)
		}

		value, found, err := s.parseValueFromStart(start)
		if err != nil {
			return "", fmt.Errorf("scan json value: %w", err)
		}
		if found {
			return value, nil
		}
	}
}
