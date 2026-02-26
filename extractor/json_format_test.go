package extractor_test

import (
	"bytes"
	"encoding/json"
)

func mustPrettyJSON(payload string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(payload), "", "  "); err != nil {
		panic("invalid json fixture: " + err.Error())
	}
	return out.String()
}
