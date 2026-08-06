package provider

import "strings"

// IsContextOverflowError reports whether err indicates that the provider
// rejected a request because its payload or tokenized input exceeds the
// serving model's context limit. Provider responses use different HTTP status
// codes, error codes, and prose, so callers use this semantic classification
// when deciding whether compaction is a safe recovery attempt.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return hasHTTPStatus(msg, "413") ||
		strings.Contains(msg, "payload too large") ||
		strings.Contains(msg, "request entity too large") ||
		strings.Contains(msg, "input exceeds the context window") ||
		strings.Contains(msg, "context window exceeded") ||
		strings.Contains(msg, "maximum context length") ||
		strings.Contains(msg, "context_length_exceeded") ||
		(strings.Contains(msg, "input") && strings.Contains(msg, "exceeds the maximum number of tokens"))
}

func hasHTTPStatus(message, code string) bool {
	fields := strings.Fields(message)
	for i, field := range fields {
		if strings.Trim(field, "():,;[]{}") != code {
			continue
		}
		if i == 0 {
			return true
		}
		for offset := 1; offset <= 2 && i-offset >= 0; offset++ {
			prefix := strings.Trim(fields[i-offset], "():,;[]{}")
			if prefix == "http" || prefix == "status" || prefix == "response" {
				return true
			}
		}
	}
	return false
}
