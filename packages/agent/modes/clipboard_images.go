package modes

import (
	"strings"

	"github.com/bnema/zut/packages/provider"
)

type clipboardImageAttachment struct {
	Marker string
	Image  provider.ImageBlock
}

func preparePromptWithClipboardImages(text string, pending []clipboardImageAttachment) (string, []provider.ImageBlock) {
	if len(pending) == 0 {
		return text, nil
	}

	out := text
	images := make([]provider.ImageBlock, 0, len(pending))
	for _, item := range pending {
		if item.Marker == "" || !strings.Contains(out, item.Marker) {
			continue
		}
		out = removeClipboardMarker(out, item.Marker)
		images = append(images, item.Image)
	}
	return strings.TrimSpace(out), images
}

func removeClipboardMarker(text, marker string) string {
	for {
		idx := strings.Index(text, marker)
		if idx < 0 {
			return text
		}
		end := idx + len(marker)
		prevInline := idx > 0 && isInlineWhitespace(text[idx-1])
		nextInline := end < len(text) && isInlineWhitespace(text[end])
		prevLineBreak := idx == 0 || idx > 0 && isLineBreak(text[idx-1])
		nextLineBreak := end == len(text) || end < len(text) && isLineBreak(text[end])
		switch {
		case prevInline && nextInline:
			text = text[:idx] + text[end+1:]
		case prevInline && nextLineBreak:
			text = text[:idx-1] + text[end:]
		case prevLineBreak && nextInline:
			text = text[:idx] + text[end+1:]
		default:
			text = text[:idx] + text[end:]
		}
	}
}

func isInlineWhitespace(b byte) bool {
	return b == ' ' || b == '\t'
}

func isLineBreak(b byte) bool {
	return b == '\n' || b == '\r'
}
