package credentialpolicy

import (
	"bytes"
	"encoding/json"
	"io"
)

// A malformed-operation response is not transient Core unavailability and never
// authorizes rewriting/replacing an immutable operation under its old ID.
func DecodeGateBadRequest(raw []byte) string {
	invalid := "gate_request_invalid"
	if len(raw) > 4096 {
		return invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid
	}
	if key, err := d.Token(); err != nil || key != "error" {
		return invalid
	}
	if value, err := d.Token(); err != nil || value != "invalid_operation" {
		return invalid
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid
	}
	return "gate_operation_invalid"
}
