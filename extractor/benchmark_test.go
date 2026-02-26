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

const (
	kibibyte = 1 << 10
	mebibyte = 1 << 20

	largeBodySizeBytes = 15 * mebibyte
)

type expected struct {
	value string
	err   error
}

func runBenchmark(b *testing.B, payload []byte, wants map[string]expected) {
	extractors := []extractor.Extractor{
		decoder.Extractor{},
		fastjson.Extractor{},
		gjson.Extractor{},
		structs.Extractor{},
	}

	for _, ext := range extractors {
		ext := ext
		want := wants[ext.Name()]
		b.Run(ext.Name(), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, err := ext.Extract(bytes.NewReader(payload))
				if want.err != nil {
					if !errors.Is(err, want.err) {
						b.Fatalf("expected error %v, got %v", want.err, err)
					}
					continue
				}

				if err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
				if got != want.value {
					b.Fatalf("unexpected value: got %q want %q", got, want.value)
				}
			}
		})
	}
}

func BenchmarkExtract_ChannelEarly_Small(b *testing.B) {
	payload := []byte(`{"channel":"ios","body":"hello world"}`)
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "ios"},
		"fastjson": {value: "ios"},
		"gjson":    {value: "ios"},
		"struct":   {value: "ios"},
	})
}

func BenchmarkExtract_ChannelEarly_Small_Pretty(b *testing.B) {
	rawPayload := `{"channel":"ios","body":"hello world"}`
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "ios"},
		"fastjson": {value: "ios"},
		"gjson":    {value: "ios"},
		"struct":   {value: "ios"},
	})
}

func BenchmarkExtract_ChannelEarly_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"channel":"ios","body":%q}`, largeBody))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "ios"},
		"fastjson": {value: "ios"},
		"gjson":    {value: "ios"},
		"struct":   {value: "ios"},
	})
}

func BenchmarkExtract_ChannelEarly_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"channel":"ios","body":%q}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "ios"},
		"fastjson": {value: "ios"},
		"gjson":    {value: "ios"},
		"struct":   {value: "ios"},
	})
}

func BenchmarkExtract_ChannelLate_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"body":%q,"channel":"android"}`, largeBody))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "android"},
		"fastjson": {value: "android"},
		"gjson":    {value: "android"},
		"struct":   {value: "android"},
	})
}

func BenchmarkExtract_ChannelLate_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"body":%q,"channel":"android"}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "android"},
		"fastjson": {value: "android"},
		"gjson":    {value: "android"},
		"struct":   {value: "android"},
	})
}

func BenchmarkExtract_ChannelNested_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"meta":{"channel":"email"},"body":%q}`, largeBody))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "email"},
		"fastjson": {err: extractor.ErrChannelNotFound},
		"gjson":    {err: extractor.ErrChannelNotFound},
		"struct":   {err: extractor.ErrChannelNotFound},
	})
}

func BenchmarkExtract_ChannelNested_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"meta":{"channel":"email"},"body":%q}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {value: "email"},
		"fastjson": {err: extractor.ErrChannelNotFound},
		"gjson":    {err: extractor.ErrChannelNotFound},
		"struct":   {err: extractor.ErrChannelNotFound},
	})
}

func BenchmarkExtract_NotFound_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"body":%q,"meta":{"source":"ingest"}}`, largeBody))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {err: extractor.ErrChannelNotFound},
		"fastjson": {err: extractor.ErrChannelNotFound},
		"gjson":    {err: extractor.ErrChannelNotFound},
		"struct":   {err: extractor.ErrChannelNotFound},
	})
}

func BenchmarkExtract_NotFound_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"body":%q,"meta":{"source":"ingest"}}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, map[string]expected{
		"decoder":  {err: extractor.ErrChannelNotFound},
		"fastjson": {err: extractor.ErrChannelNotFound},
		"gjson":    {err: extractor.ErrChannelNotFound},
		"struct":   {err: extractor.ErrChannelNotFound},
	})
}
