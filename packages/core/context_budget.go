package core

import (
	"unicode/utf8"

	"github.com/bnema/zut/packages/provider"
)

const (
	maxToolResultTextBytes      = 32 * 1024
	maxToolResultTotalTextBytes = 128 * 1024
	toolResultOmissionMarker    = "[tool result content omitted]"
)

// projectToolResultMessages returns a copied provider-input view of msgs.
// Text inside each tool result is bounded independently and then against the
// total budget, with newer results receiving the remaining budget first.
// Tool-result blocks and all non-text content remain in the projection so
// tool-call/result pairing stays valid.
func projectToolResultMessages(msgs []provider.Message) []provider.Message {
	projected := copyToolResultMessages(msgs)
	remaining := maxToolResultTotalTextBytes

	for i := len(projected) - 1; i >= 0; i-- {
		for j := len(projected[i].Content) - 1; j >= 0; j-- {
			result, ok := projected[i].Content[j].(provider.ToolResultBlock)
			if !ok {
				continue
			}

			textBytes := toolResultTextBytes(result.Content)
			if textBytes == 0 {
				continue
			}

			limit := maxToolResultTextBytes
			if remaining < limit {
				limit = remaining
			}
			var retained int
			result.Content, retained = projectToolResultContent(result.Content, limit)
			projected[i].Content[j] = result
			remaining -= retained
		}
	}

	return projected
}

// copyToolResultMessages copies the slices that the projection can replace.
// Content blocks are values, so sharing their immutable fields is safe; the
// nested content slice of every ToolResultBlock is copied before projection.
func copyToolResultMessages(msgs []provider.Message) []provider.Message {
	if msgs == nil {
		return nil
	}
	out := make([]provider.Message, len(msgs))
	for i, message := range msgs {
		out[i] = message
		if message.Content != nil {
			out[i].Content = append([]provider.Content(nil), message.Content...)
		}
		if message.AddedToolNames != nil {
			out[i].AddedToolNames = append([]string(nil), message.AddedToolNames...)
		}
		if message.Meta != nil {
			out[i].Meta = make(map[string]string, len(message.Meta))
			for key, value := range message.Meta {
				out[i].Meta[key] = value
			}
		}
		for j, content := range out[i].Content {
			if result, ok := content.(provider.ToolResultBlock); ok {
				result.Content = append([]provider.Content(nil), result.Content...)
				out[i].Content[j] = result
			}
		}
	}
	return out
}

func toolResultTextBytes(content []provider.Content) int {
	total := 0
	for _, block := range content {
		if text, ok := block.(provider.TextBlock); ok {
			total += len(text.Text)
		}
	}
	return total
}

// projectToolResultContent projects one result's textual content to limit
// bytes. Every truncated result receives an omission marker, even after the
// aggregate content budget is exhausted. The returned count excludes that
// marker so the budget covers retained tool output rather than its notices.
// Non-text blocks are retained in their original order.
func projectToolResultContent(content []provider.Content, limit int) ([]provider.Content, int) {
	textBytes := toolResultTextBytes(content)
	if textBytes <= limit {
		return append([]provider.Content(nil), content...), textBytes
	}

	out := make([]provider.Content, 0, len(content)+1)
	prefixRemaining := limit
	prefixUsed := 0
	truncated := false
	for _, block := range content {
		text, ok := block.(provider.TextBlock)
		if !ok {
			out = append(out, block)
			continue
		}
		if truncated {
			continue
		}
		if len(text.Text) <= prefixRemaining {
			out = append(out, text)
			prefixRemaining -= len(text.Text)
			prefixUsed += len(text.Text)
			continue
		}

		text.Text = utf8Prefix(text.Text, prefixRemaining)
		if text.Text != "" {
			out = append(out, text)
		}
		prefixUsed += len(text.Text)
		truncated = true
	}
	out = append(out, provider.TextBlock{Text: toolResultOmissionMarker})
	return out, prefixUsed
}

func utf8Prefix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if limit >= len(text) {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
