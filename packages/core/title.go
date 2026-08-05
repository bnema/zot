package core

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/bnema/zut/packages/provider"
)

// SessionTitleMaxChars is the maximum length of an automatically generated
// session title. The limit is measured in Unicode code points so a title never
// cuts a UTF-8 sequence in half.
const SessionTitleMaxChars = 40

const sessionTitleMaxOutputTokens = 16
const sessionTitlePromptMaxChars = 12000

const sessionTitleSystemPrompt = `You create concise titles for coding sessions.
Return only the subject title for the user's task. Maximum 40 characters. Use one
line. Do not include quotes, Markdown, explanations, or trailing punctuation.`

// GenerateSessionTitle asks the provider for a short title without touching a
// conversation transcript. It is intentionally separate from Agent.Prompt:
// title generation is UI/session metadata, not an assistant turn.
func GenerateSessionTitle(ctx context.Context, client provider.Client, model, prompt string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("title generation: provider client is nil")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("title generation: prompt is empty")
	}

	promptRunes := []rune(prompt)
	if len(promptRunes) > sessionTitlePromptMaxChars {
		prompt = string(promptRunes[:sessionTitlePromptMaxChars])
	}
	stream, err := client.Stream(ctx, provider.Request{
		Model:     model,
		System:    sessionTitleSystemPrompt,
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: prompt}}}},
		MaxTokens: sessionTitleMaxOutputTokens,
	})
	if err != nil {
		return "", fmt.Errorf("title generation request: %w", err)
	}

	var (
		text     strings.Builder
		done     bool
		finalErr error
		finalMsg provider.Message
	)
	streamOpen := true
	for streamOpen && !done {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("title generation stream: %w", ctx.Err())
		case event, ok := <-stream:
			if !ok {
				streamOpen = false
				continue
			}
			switch e := event.(type) {
			case provider.EventTextDelta:
				text.WriteString(e.Delta)
			case provider.EventDone:
				done = true
				finalErr = e.Err
				finalMsg = e.Message
			}
		}
	}
	if finalErr != nil {
		return "", fmt.Errorf("title generation stream: %w", finalErr)
	}
	if !done {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("title generation stream: %w", err)
		}
		return "", fmt.Errorf("title generation stream ended without a result")
	}
	if text.Len() == 0 {
		text.WriteString(textFromMessage(finalMsg))
	}
	if title := NormalizeSessionTitle(text.String()); title != "" {
		return title, nil
	}
	return "", fmt.Errorf("title generation returned an empty title")
}

// NormalizeSessionTitle turns model or user text into one safe, compact
// session-title value. It is also used for local fallbacks before a title is
// sent to a terminal escape sequence.
func NormalizeSessionTitle(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x7f {
			return -1
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "\"'`")
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"title:", "subject:"} {
		if len(text) >= len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) {
			text = strings.TrimSpace(text[len(prefix):])
			break
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, "\"'`")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > SessionTitleMaxChars {
		if SessionTitleMaxChars == 1 {
			return string(runes[:1])
		}
		return string(runes[:SessionTitleMaxChars-1]) + "…"
	}
	return text
}

func textFromMessage(message provider.Message) string {
	var text strings.Builder
	for _, content := range message.Content {
		if block, ok := content.(provider.TextBlock); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}
