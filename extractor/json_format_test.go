package extractor_test

import (
	"bytes"
	"encoding/json"

	"github.com/cruffinoni/go-json-finder/extractors/decoder"
)

func mustPrettyJSON(payload string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(payload), "", "  "); err != nil {
		panic("invalid json fixture: " + err.Error())
	}
	return out.String()
}

func mustNewExtractor(key string) decoder.Extractor {
	ext, err := decoder.NewExtractor(key)
	if err != nil {
		panic("mustNewExtractor: " + err.Error())
	}
	return ext
}
