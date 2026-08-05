package tui

import (
	"strings"
	"testing"

	"github.com/bnema/zut/packages/provider"
)

func userMsg(text string) provider.Message {
	return provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: text}},
	}
}

// userBubbleRows counts the rows that carry the user gutter bar "▌".
func userBubbleRows(plain string) int {
	n := 0
	for _, l := range strings.Split(plain, "\n") {
		if strings.Contains(l, "▌") {
			n++
		}
	}
	return n
}

// A one-line prompt is a 3-row bubble by default (blank pad, text,
// blank pad); compact mode collapses it to a single text row.
func TestCompactUserDropsPaddingRows(t *testing.T) {
	bubble := View{Theme: Dark, Messages: []provider.Message{userMsg("hello")}}
	bubblePlain := stripANSI(strings.Join(bubble.Build(80), "\n"))
	if got := userBubbleRows(bubblePlain); got != 3 {
		t.Fatalf("default bubble should be 3 gutter rows (pad+text+pad), got %d:\n%s", got, bubblePlain)
	}

	compact := View{Theme: Dark, CompactUser: true, Messages: []provider.Message{userMsg("hello")}}
	compactPlain := stripANSI(strings.Join(compact.Build(80), "\n"))
	if got := userBubbleRows(compactPlain); got != 1 {
		t.Fatalf("compact user should be a single gutter row, got %d:\n%s", got, compactPlain)
	}
	if !strings.Contains(compactPlain, "▌ hello") {
		t.Fatalf("compact user lost the gutter or text:\n%s", compactPlain)
	}
}

// Compact mode must never paint the bubble background tint. The tint
// is an SGR background sequence (ESC[48;...m); the compact path uses
// only a foreground-colored gutter, so no 48; should appear on the
// user rows.
func TestCompactUserHasNoBackgroundTint(t *testing.T) {
	compact := View{Theme: Dark, CompactUser: true, Messages: []provider.Message{userMsg("hi there")}}
	raw := strings.Join(compact.Build(80), "\n")
	if strings.Contains(raw, "[48;") {
		t.Fatalf("compact user should not paint a background tint:\n%q", raw)
	}

	// The default bubble does tint, so this is a meaningful assertion.
	bubble := View{Theme: Dark, Messages: []provider.Message{userMsg("hi there")}}
	if rawB := strings.Join(bubble.Build(80), "\n"); !strings.Contains(rawB, "[48;") {
		t.Fatalf("default bubble was expected to paint a background tint:\n%q", rawB)
	}
}

// Multi-line prompts keep every wrapped row in compact mode, just
// without the surrounding padding rows.
func TestCompactUserKeepsAllWrappedRows(t *testing.T) {
	long := strings.Repeat("word ", 60) // forces several wrapped rows
	compact := View{Theme: Dark, CompactUser: true, Messages: []provider.Message{userMsg(long)}}
	plain := stripANSI(strings.Join(compact.Build(40), "\n"))
	rows := userBubbleRows(plain)
	if rows < 3 {
		t.Fatalf("expected multiple wrapped gutter rows, got %d:\n%s", rows, plain)
	}
	// No empty gutter rows (that would be leftover padding).
	for _, l := range strings.Split(plain, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "▌")) == "" && strings.Contains(l, "▌") {
			t.Fatalf("compact user emitted an empty padding row:\n%s", plain)
		}
	}
}
