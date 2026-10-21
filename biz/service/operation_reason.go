package service

import (
	"strings"
	"unicode/utf8"
)

const operationReasonMax = 500

// normalizeOptionalReason shares the optional note contract across management operations.
func normalizeOptionalReason(raw string) (string, bool) {
	reason := strings.TrimSpace(raw)
	return reason, utf8.RuneCountInString(reason) <= operationReasonMax
}
