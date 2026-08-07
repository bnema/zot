package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkRendererDrawLogToolStorm(b *testing.B) {
	for _, width := range []int{48, 80, 160} {
		b.Run(fmt.Sprintf("width=%d", width), func(b *testing.B) {
			var out bytes.Buffer
			r := NewRenderer(&out)
			r.Resize(width, 40)
			chat := benchmarkRendererChat(1600, width)
			bottom := []string{"working", "▌ input"}
			r.DrawLog(chat, bottom, 1, 2)
			out.Reset()

			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				// Keep most of the storm stable while exercising the
				// renderer's row-diff path with a changing suffix.
				frame := append([]string(nil), chat...)
				frame[len(frame)-1] = fmt.Sprintf("tool output %d", n)
				r.DrawLog(frame, bottom, 1, 2)
				out.Reset()
			}
		})
	}
}

func benchmarkRendererChat(rows, width int) []string {
	chat := make([]string, rows)
	payload := strings.Repeat("x", width)
	for i := range chat {
		chat[i] = fmt.Sprintf("│ tool-%03d result %s", i, payload)
	}
	return chat
}
