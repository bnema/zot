package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/provider"
)

func BenchmarkViewBuildWithAnchorsToolStorm(b *testing.B) {
	for _, width := range []int{48, 80, 160} {
		for _, expanded := range []bool{false, true} {
			for _, temperature := range []string{"cold", "warm"} {
				name := fmt.Sprintf("width=%d/expanded=%t/%s", width, expanded, temperature)
				b.Run(name, func(b *testing.B) {
					base := benchmarkToolStormView(expanded)
					if temperature == "warm" {
						base.BuildWithAnchors(width)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						view := base
						if temperature == "cold" {
							view.InvalidateRenderCache()
						}
						lines, anchors := view.BuildWithAnchors(width)
						if len(lines) == 0 || len(anchors) == 0 {
							b.Fatal("tool storm produced no rows or anchors")
						}
					}
				})
			}
		}
	}
}

func BenchmarkViewBuildWithAnchorsLiveToolStorm(b *testing.B) {
	for _, width := range []int{48, 80, 160} {
		for _, expanded := range []bool{false, true} {
			name := "width=" + strconv.Itoa(width) + "/expanded=" + strconv.FormatBool(expanded)
			b.Run(name, func(b *testing.B) {
				view := &View{
					Theme:            Dark,
					ExpandAll:        expanded,
					Messages:         benchmarkToolStormMessages(24, 8),
					MessagesRevision: 1,
					ToolCalls:        benchmarkLiveTools(8, 96),
				}
				liveResults := make([]string, len(view.ToolCalls))
				for i := range liveResults {
					liveResults[i] = benchmarkResult(96, i)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					call := &view.ToolCalls[n%len(view.ToolCalls)]
					call.Revision++
					call.Result = liveResults[n%len(liveResults)]
					lines, anchors := view.BuildWithAnchors(width)
					if len(lines) == 0 || len(anchors) == 0 {
						b.Fatal("live tool storm produced no rows or anchors")
					}
				}
			})
		}
	}
}

func benchmarkToolStormView(expanded bool) *View {
	return &View{
		Theme:            Dark,
		ExpandAll:        expanded,
		Messages:         benchmarkToolStormMessages(48, 96),
		MessagesRevision: 1,
	}
}

func benchmarkToolStormMessages(tools, resultLines int) []provider.Message {
	messages := make([]provider.Message, 0, tools*3)
	for i := 0; i < tools; i++ {
		id := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			provider.Message{
				Role: provider.RoleUser,
				Content: []provider.Content{provider.TextBlock{
					Text: fmt.Sprintf("inspect result %d", i),
				}},
			},
			provider.Message{
				Role: provider.RoleAssistant,
				Content: []provider.Content{provider.ToolCallBlock{
					ID:        id,
					Name:      "bash",
					Arguments: []byte(`{"command":"printf storm"}`),
				}},
			},
			provider.Message{
				Role: provider.RoleTool,
				Content: []provider.Content{provider.ToolResultBlock{
					CallID:  id,
					Content: []provider.Content{provider.TextBlock{Text: benchmarkResult(resultLines, i)}},
				}},
			},
		)
	}
	return messages
}

func benchmarkLiveTools(tools, resultLines int) []ToolCallView {
	out := make([]ToolCallView, 0, tools)
	for i := 0; i < tools; i++ {
		out = append(out, ToolCallView{
			ID:       fmt.Sprintf("live-%d", i),
			Name:     "bash",
			Revision: 1,
			Args:     "printf storm",
			Result:   benchmarkResult(resultLines, i),
			Done:     true,
		})
	}
	return out
}

func benchmarkResult(lines, tool int) string {
	var b strings.Builder
	for line := 0; line < lines; line++ {
		fmt.Fprintf(&b, "tool=%d line=%03d output payload for highlighting and wrapping\n", tool, line)
	}
	return b.String()
}
