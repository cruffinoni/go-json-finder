package decoder

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cruffinoni/go-json-finder/extractor"
)

func newScannerWithBuffer(input string, bufferSize int) scanner {
	return scanner{r: bufio.NewReaderSize(strings.NewReader(input), bufferSize)}
}

type readSpy struct {
	src    []byte
	off    int
	maxReq int
}

func (r *readSpy) Read(p []byte) (int, error) {
	if len(p) > r.maxReq {
		r.maxReq = len(p)
	}
	if r.off >= len(r.src) {
		return 0, io.EOF
	}
	n := copy(p, r.src[r.off:])
	r.off += n
	return n, nil
}

func TestFindStringStop(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		wantIndex   int
		wantSpecial byte
	}{
		{
			name:      "no special byte",
			input:     []byte("abcdefghijklmnopqrstuvwxyz"),
			wantIndex: -1,
		},
		{
			name:        "quote terminator",
			input:       []byte("abc\"tail"),
			wantIndex:   3,
			wantSpecial: '"',
		},
		{
			name:        "backslash before quote",
			input:       []byte("abc\\\"tail"),
			wantIndex:   3,
			wantSpecial: '\\',
		},
		{
			name:        "control character before markers",
			input:       []byte{'a', 'b', 0x1f, '"'},
			wantIndex:   2,
			wantSpecial: 0x1f,
		},
		{
			name:        "boundary across 8 byte block",
			input:       []byte("abcdefgh\"tail"),
			wantIndex:   8,
			wantSpecial: '"',
		},
		{
			name:        "earliest candidate in same block",
			input:       []byte{'x', '\\', '"', 0x1f, 'z'},
			wantIndex:   1,
			wantSpecial: '\\',
		},
	}

	s := scanner{}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			index, special := s.findStringStop(tt.input)
			if index != tt.wantIndex {
				t.Fatalf("unexpected index: got %d want %d", index, tt.wantIndex)
			}
			if special != tt.wantSpecial {
				t.Fatalf("unexpected special byte: got %q want %q", special, tt.wantSpecial)
			}
		})
	}
}

func TestCappedReader(t *testing.T) {
	base := strings.NewReader("abcdefghijkl")
	cr := &cappedReader{}
	cr.reset(base, 4)

	buf := make([]byte, 10)
	n, err := cr.Read(buf)
	if err != nil {
		t.Fatalf("first read returned error: %v", err)
	}
	if n != 4 {
		t.Fatalf("unexpected first read length: got %d want 4", n)
	}
	if string(buf[:n]) != "abcd" {
		t.Fatalf("unexpected first read content: got %q", string(buf[:n]))
	}

	n, err = cr.Read(buf)
	if err != nil {
		t.Fatalf("second read returned error: %v", err)
	}
	if n != 4 {
		t.Fatalf("unexpected second read length: got %d want 4", n)
	}
	if string(buf[:n]) != "efgh" {
		t.Fatalf("unexpected second read content: got %q", string(buf[:n]))
	}

	cr.setMaxReadChunk(8)
	n, err = cr.Read(buf)
	if err != nil {
		t.Fatalf("third read returned error: %v", err)
	}
	if n != 4 {
		t.Fatalf("unexpected third read length: got %d want 4", n)
	}
	if string(buf[:n]) != "ijkl" {
		t.Fatalf("unexpected third read content: got %q", string(buf[:n]))
	}
}

func TestExtractorReadCapBehavior(t *testing.T) {
	ext, err := NewExtractor("channel")
	if err != nil {
		t.Fatalf("NewExtractor returned unexpected error: %v", err)
	}

	t.Run("early channel keeps initial cap", func(t *testing.T) {
		payload := []byte(`{"channel":"ios","body":"` + strings.Repeat("x", 256*1024) + `"}`)
		spy := &readSpy{src: payload}

		got, err := ext.Extract(spy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "ios" {
			t.Fatalf("unexpected channel: got %q want %q", got, "ios")
		}
		if spy.maxReq > initialReadChunkCap {
			t.Fatalf("unexpected max read size for early path: got %d > %d", spy.maxReq, initialReadChunkCap)
		}
	})

	t.Run("late channel promotes to larger cap", func(t *testing.T) {
		payload := []byte(`{"body":"` + strings.Repeat("x", 256*1024) + `","channel":"ios"}`)
		spy := &readSpy{src: payload}

		got, err := ext.Extract(spy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "ios" {
			t.Fatalf("unexpected channel: got %q want %q", got, "ios")
		}
		if spy.maxReq <= initialReadChunkCap {
			t.Fatalf("expected read cap promotion; max read request stayed at %d", spy.maxReq)
		}
		if spy.maxReq > lateReadChunkCap {
			t.Fatalf("unexpected max read size after promotion: got %d > %d", spy.maxReq, lateReadChunkCap)
		}
	})
}

func TestReadString(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		bufferSize   int
		wantValue    string
		wantErrSub   string
		checkNext    bool
		wantNextByte byte
	}{
		{
			name:         "backslash at chunk boundary",
			input:        `abc\"def",`,
			bufferSize:   4,
			wantValue:    `abc"def`,
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:         "unicode surrogate pair",
			input:        `\uD83D\uDE00",`,
			bufferSize:   5,
			wantValue:    "😀",
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:         "escaped solidus and control escapes",
			input:        `a\/b\n\t\r\f\b",`,
			bufferSize:   4,
			wantValue:    "a/b\n\t\r\f\b",
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:       "invalid escape sequence",
			input:      `abc\x",`,
			bufferSize: 4,
			wantErrSub: "invalid escape sequence",
		},
		{
			name:       "unexpected low surrogate",
			input:      `\uDC00",`,
			bufferSize: 4,
			wantErrSub: "unexpected low surrogate",
		},
		{
			name:       "high surrogate without low surrogate",
			input:      `\uD83Dx",`,
			bufferSize: 4,
			wantErrSub: "expected low surrogate after high surrogate",
		},
		{
			name:       "control character is rejected",
			input:      "ab" + string([]byte{0x1f}) + `c"`,
			bufferSize: 4,
			wantErrSub: "invalid control character in string",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := newScannerWithBuffer(tt.input, tt.bufferSize)

			value, err := s.readString()
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got value %q", tt.wantErrSub, value)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("readString returned error: %v", err)
			}
			if value != tt.wantValue {
				t.Fatalf("unexpected value: got %q want %q", value, tt.wantValue)
			}

			if tt.checkNext {
				next, err := s.r.ReadByte()
				if err != nil {
					t.Fatalf("failed to read next byte: %v", err)
				}
				if next != tt.wantNextByte {
					t.Fatalf("unexpected trailing byte: got %q want %q", next, tt.wantNextByte)
				}
			}
		})
	}
}

func TestReadKeyEquals(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		bufferSize   int
		wantMatched  bool
		wantErrSub   string
		checkNext    bool
		wantNextByte byte
	}{
		{
			name:         "plain exact match",
			input:        `channel":`,
			bufferSize:   4,
			wantMatched:  true,
			checkNext:    true,
			wantNextByte: ':',
		},
		{
			name:         "escaped ASCII match",
			input:        `chann\u0065l":`,
			bufferSize:   4,
			wantMatched:  true,
			checkNext:    true,
			wantNextByte: ':',
		},
		{
			name:         "unicode non ASCII mismatch",
			input:        `chann\u00E9l":`,
			bufferSize:   4,
			wantMatched:  false,
			checkNext:    true,
			wantNextByte: ':',
		},
		{
			name:         "mismatch still consumes string",
			input:        `channelx":`,
			bufferSize:   4,
			wantMatched:  false,
			checkNext:    true,
			wantNextByte: ':',
		},
		{
			name:       "invalid escape in key",
			input:      `chan\xnel":`,
			bufferSize: 4,
			wantErrSub: "invalid escape sequence",
		},
		{
			name:       "unterminated key",
			input:      `channel`,
			bufferSize: 4,
			wantErrSub: "unexpected EOF while reading string",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := newScannerWithBuffer(tt.input, tt.bufferSize)

			matched, err := s.readKeyEquals("channel")
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got matched=%v", tt.wantErrSub, matched)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("readKeyEquals returned error: %v", err)
			}
			if matched != tt.wantMatched {
				t.Fatalf("unexpected match: got %v want %v", matched, tt.wantMatched)
			}

			if tt.checkNext {
				next, err := s.r.ReadByte()
				if err != nil {
					t.Fatalf("failed to read next byte: %v", err)
				}
				if next != tt.wantNextByte {
					t.Fatalf("unexpected trailing byte: got %q want %q", next, tt.wantNextByte)
				}
			}
		})
	}
}

func TestSkipString(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		bufferSize   int
		wantErrSub   string
		checkNext    bool
		wantNextByte byte
	}{
		{
			name:         "stops on closing quote",
			input:        `abc",tail`,
			bufferSize:   4,
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:         "handles escaped quote",
			input:        `abc\"def",tail`,
			bufferSize:   4,
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:       "invalid escape is rejected",
			input:      `abc\x",tail`,
			bufferSize: 4,
			wantErrSub: "invalid escape sequence",
		},
		{
			name:       "invalid unicode hex is rejected",
			input:      `abc\u12G4",tail`,
			bufferSize: 4,
			wantErrSub: "invalid hex digit",
		},
		{
			name:       "control character is rejected",
			input:      "ab" + string([]byte{0x1f}) + `c"`,
			bufferSize: 4,
			wantErrSub: "invalid control character in string",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := newScannerWithBuffer(tt.input, tt.bufferSize)

			err := s.skipString()
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("skipString returned error: %v", err)
			}

			if tt.checkNext {
				next, err := s.r.ReadByte()
				if err != nil {
					t.Fatalf("failed to read next byte: %v", err)
				}
				if next != tt.wantNextByte {
					t.Fatalf("unexpected trailing byte: got %q want %q", next, tt.wantNextByte)
				}
			}
		})
	}
}

func TestExtractorExtract(t *testing.T) {
	tests := []struct {
		name            string
		payload         string
		wantValue       string
		wantErr         error
		wantAnyParseErr bool
	}{
		{
			name:      "top level channel string",
			payload:   `{"channel":"ios"}`,
			wantValue: "ios",
		},
		{
			name:      "top level channel with whitespace",
			payload:   " \n\t {\n\t\"channel\" : \"android\"\n } ",
			wantValue: "android",
		},
		{
			name:      "nested channel is ignored in favor of top level key",
			payload:   `{"meta":{"channel":"email"},"channel":"ios"}`,
			wantValue: "ios",
		},
		{
			name:      "escaped nested channel key is ignored",
			payload:   `{"meta":{"chann\u0065l":"sms"},"channel":"ios"}`,
			wantValue: "ios",
		},
		{
			name:      "channel key in string is ignored",
			payload:   `{"body":"... here is text: \\\"channel\\\":\\\"ios\\\" ...","channel":"email"}`,
			wantValue: "email",
		},
		{
			name:      "non channel key is skipped before next key",
			payload:   `{"body":"hello","channel":"sms"}`,
			wantValue: "sms",
		},
		{
			name:      "non channel nested array is skipped before next key",
			payload:   `{"meta":[1,2,3],"channel":"ios"}`,
			wantValue: "ios",
		},
		{
			name:      "non channel nested object with comma is skipped before next key",
			payload:   `{"meta":{"a":1,"b":2},"channel":"push"}`,
			wantValue: "push",
		},
		{
			name:    "channel number invalid type",
			payload: `{"channel":123}`,
			wantErr: extractor.ErrChannelInvalidType,
		},
		{
			name:    "channel boolean invalid type",
			payload: `{"channel":true}`,
			wantErr: extractor.ErrChannelInvalidType,
		},
		{
			name:    "channel null invalid type",
			payload: `{"channel":null}`,
			wantErr: extractor.ErrChannelInvalidType,
		},
		{
			name:    "channel object invalid type",
			payload: `{"channel":{}}`,
			wantErr: extractor.ErrChannelInvalidType,
		},
		{
			name:    "channel array invalid type",
			payload: `{"channel":[1,2,3]}`,
			wantErr: extractor.ErrChannelInvalidType,
		},
		{
			name:    "missing channel",
			payload: `{"body":"hello"}`,
			wantErr: extractor.ErrChannelNotFound,
		},
		{
			name:    "missing channel with trailing whitespace",
			payload: "{\"body\":\"hello\"}  \n\t",
			wantErr: extractor.ErrChannelNotFound,
		},
		{
			name:            "missing channel with trailing non space returns parse error",
			payload:         `{"body":"hello"} x`,
			wantAnyParseErr: true,
		},
		{
			name:      "skipped high surrogate without low surrogate is tolerated",
			payload:   `{"body":"\uD83Dx","channel":"ok"}`,
			wantValue: "ok",
		},
		{
			name:            "invalid escape in skipped value returns parse error",
			payload:         `{"body":"bad\x","channel":"ok"}`,
			wantAnyParseErr: true,
		},
		{
			name:            "invalid control character in skipped composite string returns parse error",
			payload:         "{\"meta\":{\"body\":\"ab" + string([]byte{0x1f}) + "c\"},\"channel\":\"ok\"}",
			wantAnyParseErr: true,
		},
		{
			name:            "invalid unicode hex in skipped value returns parse error",
			payload:         `{"body":"bad\u12G4","channel":"ok"}`,
			wantAnyParseErr: true,
		},
		{
			name:            "strict channel string still rejects high surrogate without low surrogate",
			payload:         `{"channel":"\uD83Dx"}`,
			wantAnyParseErr: true,
		},
		{
			name:            "invalid json returns parse error",
			payload:         `{"body":"unterminated}`,
			wantAnyParseErr: true,
		},
		{
			name:            "multiple top level values return parse error in strict mode",
			payload:         `{} {"channel":"push"}`,
			wantAnyParseErr: true,
		},
		{
			name:      "channel match returns before trailing validation",
			payload:   `{"channel":"push"} trailing`,
			wantValue: "push",
		},
	}

	ext, err := NewExtractor("channel")
	if err != nil {
		t.Fatalf("NewExtractor returned unexpected error: %v", err)
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ext.Extract(strings.NewReader(tt.payload))

			if tt.wantAnyParseErr {
				if err == nil {
					t.Fatalf("expected parse error, got value %q", got)
				}
				if errors.Is(err, extractor.ErrChannelNotFound) || errors.Is(err, extractor.ErrChannelInvalidType) {
					t.Fatalf("expected parse error, got sentinel error %v", err)
				}
				return
			}

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got value %q", tt.wantErr, got)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantValue {
				t.Fatalf("unexpected value: got %q want %q", got, tt.wantValue)
			}
		})
	}
}

func TestNewExtractor(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		payload      string
		wantKey      string
		wantValue    string
		wantErr      bool
		useZeroValue bool
	}{
		{
			name:    "valid key is stored",
			key:     "channel",
			wantKey: "channel",
		},
		{
			name:    "empty key returns error",
			key:     "",
			wantErr: true,
		},
		{
			name:         "zero value returns error on Extract",
			useZeroValue: true,
			payload:      `{"channel":"ios"}`,
			wantErr:      true,
		},
		{
			name:      "custom key is respected",
			key:       "routing_key",
			payload:   `{"channel":"ios","routing_key":"sms"}`,
			wantValue: "sms",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.useZeroValue {
				var e Extractor
				_, err := e.Extract(strings.NewReader(tt.payload))
				if tt.wantErr && err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			ext, err := NewExtractor(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantKey != "" && ext.key != tt.wantKey {
				t.Fatalf("unexpected key: got %q want %q", ext.key, tt.wantKey)
			}

			if tt.payload != "" {
				got, extractErr := ext.Extract(strings.NewReader(tt.payload))
				if extractErr != nil {
					t.Fatalf("unexpected error: %v", extractErr)
				}
				if got != tt.wantValue {
					t.Fatalf("unexpected value: got %q want %q", got, tt.wantValue)
				}
			}
		})
	}
}
