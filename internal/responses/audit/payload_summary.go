package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type PayloadSummary struct {
	Present        bool   `json:"present"`
	Bytes          int    `json:"bytes,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	TopLevelKind   string `json:"top_level_kind,omitempty"`
	TopLevelFields int    `json:"top_level_fields,omitempty"`
}

func SummarizeJSONPayload(payload map[string]any) PayloadSummary {
	if len(payload) == 0 {
		return PayloadSummary{Present: false}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return PayloadSummary{
			Present:        true,
			TopLevelKind:   "object",
			TopLevelFields: len(payload),
		}
	}
	sum := sha256.Sum256(data)
	return PayloadSummary{
		Present:        true,
		Bytes:          len(data),
		SHA256:         hex.EncodeToString(sum[:]),
		TopLevelKind:   "object",
		TopLevelFields: len(payload),
	}
}
