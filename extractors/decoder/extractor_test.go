package decoder

import (
	"bufio"
	"errors"
	"strings"
	"testing"

	"github.com/cruffinoni/go-json-finder/extractor"
)

func newScannerWithBuffer(input string, bufferSize int) scanner {
	return scanner{r: bufio.NewReaderSize(strings.NewReader(input), bufferSize)}
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
			name:         "backslash at chunk boundary",
			input:        `abc\"def",`,
			bufferSize:   4,
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:         "escaped unicode survives boundaries",
			input:        `abc\u0041def",`,
			bufferSize:   5,
			checkNext:    true,
			wantNextByte: ',',
		},
		{
			name:       "control character is rejected",
			input:      "ab" + string([]byte{0x1f}) + `c"`,
			bufferSize: 4,
			wantErrSub: "invalid control character in string",
		},
		{
			name:       "unexpected EOF in string",
			input:      `unterminated`,
			bufferSize: 4,
			wantErrSub: "unexpected EOF while reading string",
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

			matched, err := s.readKeyEquals(channelKey)
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
			name:      "nested channel is first structural match",
			payload:   `{"meta":{"channel":"email"},"channel":"ios"}`,
			wantValue: "email",
		},
		{
			name:      "escaped channel key is recognized",
			payload:   `{"meta":{"chann\u0065l":"sms"},"channel":"ios"}`,
			wantValue: "sms",
		},
		{
			name:      "channel key in string is ignored",
			payload:   `{"body":"... here is text: \\\"channel\\\":\\\"ios\\\" ...","channel":"email"}`,
			wantValue: "email",
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
			name:            "invalid json returns parse error",
			payload:         `{"body":"unterminated}`,
			wantAnyParseErr: true,
		},
		{
			name:      "multiple top level values are scanned in order",
			payload:   `{} {"channel":"push"}`,
			wantValue: "push",
		},
	}

	ext := Extractor{}

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
