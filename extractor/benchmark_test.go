package extractor_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cruffinoni/go-json-finder/extractor"
	"github.com/cruffinoni/go-json-finder/extractors/decoder"
	"github.com/cruffinoni/go-json-finder/extractors/easyjson"
	"github.com/cruffinoni/go-json-finder/extractors/fastjson"
	"github.com/cruffinoni/go-json-finder/extractors/gjson"
	"github.com/cruffinoni/go-json-finder/extractors/gojson"
	"github.com/cruffinoni/go-json-finder/extractors/sonic"
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

func benchmarkExpectedValues(value string) map[string]expected {
	return map[string]expected{
		"decoder":  {value: value},
		"fastjson": {value: value},
		"gjson":    {value: value},
		"go-json":  {value: value},
		"sonic":    {value: value},
		"struct":   {value: value},
		"easyjson": {value: value},
	}
}

func benchmarkExpectedErr(err error) map[string]expected {
	return map[string]expected{
		"decoder":  {err: err},
		"fastjson": {err: err},
		"gjson":    {err: err},
		"go-json":  {err: err},
		"sonic":    {err: err},
		"struct":   {err: err},
		"easyjson": {err: err},
	}
}

func runBenchmark(b *testing.B, payload []byte, wants map[string]expected) {
	extractors := []extractor.Extractor{
		decoder.Extractor{},
		fastjson.Extractor{},
		gjson.Extractor{},
		gojson.Extractor{},
		sonic.Extractor{},
		structs.Extractor{},
		easyjson.Extractor{},
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

func mixedBenchmarkPayload(channel string) string {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rootEscaped := "line1\nline2\t\"quoted\" and backslash \\"
	nestedEscaped := "nested\nvalue\twith \"quotes\" and \\slashes\\"

	return fmt.Sprintf(
		`{"id":12345,"temperature":-17.625,"scientific":6.022e23,"active":true,"deleted":false,"optional":null,"title":%q,"items":["alpha",7,2.5e1,false,null,{"kind":"leaf","ok":true}],"meta":{"source":"ingest","escaped":%q,"counts":[1,2,3],"inner":{"ratio":3.14,"threshold":9e-4,"enabled":true,"notes":null}},"body":%q,"channel":%q}`,
		rootEscaped,
		nestedEscaped,
		largeBody,
		channel,
	)
}

func BenchmarkExtract_ChannelEarly_Small(b *testing.B) {
	payload := []byte(`{"channel":"ios","body":"hello world"}`)
	runBenchmark(b, payload, benchmarkExpectedValues("ios"))
}

func BenchmarkExtract_ChannelEarly_Small_Pretty(b *testing.B) {
	rawPayload := `{"channel":"ios","body":"hello world"}`
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, benchmarkExpectedValues("ios"))
}

func BenchmarkExtract_ChannelEarly_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"channel":"ios","body":%q}`, largeBody))
	runBenchmark(b, payload, benchmarkExpectedValues("ios"))
}

func BenchmarkExtract_ChannelEarly_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"channel":"ios","body":%q}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, benchmarkExpectedValues("ios"))
}

func BenchmarkExtract_ChannelLate_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"body":%q,"channel":"android"}`, largeBody))
	runBenchmark(b, payload, benchmarkExpectedValues("android"))
}

func BenchmarkExtract_ChannelLate_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"body":%q,"channel":"android"}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, benchmarkExpectedValues("android"))
}

func BenchmarkExtract_ChannelLateMixed_Large(b *testing.B) {
	payload := []byte(mixedBenchmarkPayload("android"))
	runBenchmark(b, payload, benchmarkExpectedValues("android"))
}

func BenchmarkExtract_ChannelLateMixed_Large_Pretty(b *testing.B) {
	rawPayload := mixedBenchmarkPayload("android")
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, benchmarkExpectedValues("android"))
}

func BenchmarkExtract_NotFound_Large(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	payload := []byte(fmt.Sprintf(`{"body":%q,"meta":{"source":"ingest"}}`, largeBody))
	runBenchmark(b, payload, benchmarkExpectedErr(extractor.ErrChannelNotFound))
}

func BenchmarkExtract_NotFound_Large_Pretty(b *testing.B) {
	largeBody := strings.Repeat("x", largeBodySizeBytes)
	rawPayload := fmt.Sprintf(`{"body":%q,"meta":{"source":"ingest"}}`, largeBody)
	payload := []byte(mustPrettyJSON(rawPayload))
	runBenchmark(b, payload, benchmarkExpectedErr(extractor.ErrChannelNotFound))
}
