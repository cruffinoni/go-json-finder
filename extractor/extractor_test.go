package extractor_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cruffinoni/go-json-finder/extractor"
	"github.com/cruffinoni/go-json-finder/extractors/decoder"
	"github.com/cruffinoni/go-json-finder/extractors/fastjson"
	"github.com/cruffinoni/go-json-finder/extractors/gjson"
	"github.com/cruffinoni/go-json-finder/extractors/structs"
)

func TestExtractors(t *testing.T) {
	extractors := []extractor.Extractor{
		decoder.Extractor{},
		fastjson.Extractor{},
		gjson.Extractor{},
		structs.Extractor{},
	}

	largeBody := strings.Repeat("x", 1<<20)

	tests := map[string]struct {
		payload    string
		wantValue  map[string]string
		wantErr    map[string]error
		wantAnyErr bool
	}{
		"top level channel early": {
			payload: fmt.Sprintf(`{"channel":"ios","body":%q}`, largeBody),
			wantValue: map[string]string{
				"decoder":  "ios",
				"fastjson": "ios",
				"gjson":    "ios",
				"struct":   "ios",
			},
		},
		"top level channel early (pretty)": {
			payload: mustPrettyJSON(fmt.Sprintf(`{"channel":"ios","body":%q}`, largeBody)),
			wantValue: map[string]string{
				"decoder":  "ios",
				"fastjson": "ios",
				"gjson":    "ios",
				"struct":   "ios",
			},
		},
		"top level channel late": {
			payload: fmt.Sprintf(`{"body":%q,"channel":"android"}`, largeBody),
			wantValue: map[string]string{
				"decoder":  "android",
				"fastjson": "android",
				"gjson":    "android",
				"struct":   "android",
			},
		},
		"top level channel late (pretty)": {
			payload: mustPrettyJSON(fmt.Sprintf(`{"body":%q,"channel":"android"}`, largeBody)),
			wantValue: map[string]string{
				"decoder":  "android",
				"fastjson": "android",
				"gjson":    "android",
				"struct":   "android",
			},
		},
		"nested channel": {
			payload: `{"meta":{"channel":"email"},"body":"hello"}`,
			wantErr: map[string]error{
				"decoder":  extractor.ErrChannelNotFound,
				"fastjson": extractor.ErrChannelNotFound,
				"gjson":    extractor.ErrChannelNotFound,
				"struct":   extractor.ErrChannelNotFound,
			},
		},
		"nested channel (pretty)": {
			payload: mustPrettyJSON(`{"meta":{"channel":"email"},"body":"hello"}`),
			wantErr: map[string]error{
				"decoder":  extractor.ErrChannelNotFound,
				"fastjson": extractor.ErrChannelNotFound,
				"gjson":    extractor.ErrChannelNotFound,
				"struct":   extractor.ErrChannelNotFound,
			},
		},
		"false positive guard": {
			payload: fmt.Sprintf(`{"body":%q,"channel":"email"}`, `... here is text: \"channel\":\"ios\" ...`),
			wantValue: map[string]string{
				"decoder":  "email",
				"fastjson": "email",
				"gjson":    "email",
				"struct":   "email",
			},
		},
		"false positive guard (pretty)": {
			payload: mustPrettyJSON(fmt.Sprintf(`{"body":%q,"channel":"email"}`, `... here is text: \"channel\":\"ios\" ...`)),
			wantValue: map[string]string{
				"decoder":  "email",
				"fastjson": "email",
				"gjson":    "email",
				"struct":   "email",
			},
		},
		"invalid json": {
			payload:    `{"body":"unterminated}`,
			wantAnyErr: true,
		},
		"channel is non string": {
			payload: `{"channel":123}`,
			wantErr: map[string]error{
				"decoder":  extractor.ErrChannelInvalidType,
				"fastjson": extractor.ErrChannelInvalidType,
				"gjson":    extractor.ErrChannelInvalidType,
				"struct":   extractor.ErrChannelInvalidType,
			},
		},
		"nested non string channel does not affect top level match": {
			payload: `{"meta":{"channel":123},"channel":"android"}`,
			wantValue: map[string]string{
				"decoder":  "android",
				"fastjson": "android",
				"gjson":    "android",
				"struct":   "android",
			},
		},
		"escaped nested channel key is ignored before top level match": {
			payload: `{"meta":{"chann\u0065l":"sms"},"channel":"ios"}`,
			wantValue: map[string]string{
				"decoder":  "ios",
				"fastjson": "ios",
				"gjson":    "ios",
				"struct":   "ios",
			},
		},
		"escaped nested channel key is ignored before top level match (pretty)": {
			payload: mustPrettyJSON(`{"meta":{"chann\u0065l":"sms"},"channel":"ios"}`),
			wantValue: map[string]string{
				"decoder":  "ios",
				"fastjson": "ios",
				"gjson":    "ios",
				"struct":   "ios",
			},
		},
	}

	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			for _, ext := range extractors {
				ext := ext
				t.Run(ext.Name(), func(t *testing.T) {
					got, err := ext.Extract(bytes.NewReader([]byte(tt.payload)))
					wantErr := error(nil)
					if tt.wantErr != nil {
						wantErr = tt.wantErr[ext.Name()]
					}

					if tt.wantAnyErr {
						if err == nil {
							t.Fatalf("expected an error, got value %q", got)
						}
						if errors.Is(err, extractor.ErrChannelNotFound) || errors.Is(err, extractor.ErrChannelInvalidType) {
							t.Fatalf("expected parsing error, got %v", err)
						}
						return
					}

					if wantErr != nil {
						if err == nil {
							t.Fatalf("expected error %v, got nil", wantErr)
						}
						if !errors.Is(err, wantErr) {
							t.Fatalf("expected error %v, got %v", wantErr, err)
						}
						return
					}

					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}

					wantValue := tt.wantValue[ext.Name()]
					if got != wantValue {
						t.Fatalf("unexpected value: got %q want %q", got, wantValue)
					}
				})
			}
		})
	}
}
