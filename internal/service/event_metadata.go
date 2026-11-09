// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"encoding/json"
	"maps"
	"slices"
	"unicode/utf8"
)

const (
	maxEventMetadataBytes = 4 << 10
	maxEventFieldBytes    = 1 << 10
)

// EventMetadata builds a JSON object within the Event Log's storage budget.
// Call Add in priority order. Oversized or unencodable values can be omitted
// without losing the rest of the event.
type EventMetadata struct {
	fields map[string]json.RawMessage
	size   int
}

func (m *EventMetadata) Add(key string, value any) {
	if len(key) > maxEventFieldBytes {
		return
	}
	if s, ok := value.(string); ok && len(s) > maxEventFieldBytes {
		end := maxEventFieldBytes
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		value = s[:end]
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	if _, isString := value.(string); !isString && len(encoded) > maxEventFieldBytes {
		return
	}
	keyJSON, _ := json.Marshal(key)
	cost := len(encoded) + len(keyJSON) + len(":,")
	if previous, ok := m.fields[key]; ok {
		cost = len(encoded) - len(previous)
	}
	if m.size+cost > maxEventMetadataBytes-len("{}") {
		return
	}
	if m.fields == nil {
		m.fields = make(map[string]json.RawMessage)
	}
	m.fields[key] = encoded
	m.size += cost
}

func (m *EventMetadata) JSON() string {
	if len(m.fields) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(m.fields)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func encodeEventMetadata(values map[string]any) string {
	var metadata EventMetadata
	for _, key := range slices.Sorted(maps.Keys(values)) {
		metadata.Add(key, values[key])
	}
	return metadata.JSON()
}
