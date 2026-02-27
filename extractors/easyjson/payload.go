package easyjson

import mailrueasyjson "github.com/mailru/easyjson"

//go:generate go run github.com/mailru/easyjson/easyjson@v0.9.1 -all payload.go

//easyjson:json
type payload struct {
	Channel mailrueasyjson.RawMessage `json:"channel"`
}
