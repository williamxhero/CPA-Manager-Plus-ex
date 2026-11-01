package credentialpolicy

import (
	"bytes"
	"encoding/json"
	"io"
)

// Exact later-delta rejection contract. Opaque or malformed conflict responses
// never authorize a replacement opId. No raw error text is returned.
func DecodeGateConflict(raw []byte) string {
	var body struct {
		Error    string `json:"error"`
		Conflict string `json:"conflict"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 4096 || d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF {
		return "gate_conflict"
	}
	if body.Error == "operation_conflict" && body.Conflict == "" {
		return "gate_operation_conflict"
	}
	if body.Error == "gate_conflict" && body.Conflict == "gate_revision" {
		return "gate_revision_conflict"
	}
	return "gate_conflict"
}
