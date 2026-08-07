package tui

import (
	"bytes"
	"fmt"
	"testing"
)

func BenchmarkRendererDrawLogToolStorm(b *testing.B) {
	for _, width := range []int{48, 80, 160} {
		for _, temperature := range []string{"cold", "warm"} {
			name := fmt.Sprintf("width=%d/%s", width, temperature)
			b.Run(name, func(b *testing.B) {
				var out bytes.Buffer
				r := NewRenderer(&out)
				r.Resize(width, 40)
				chat := benchmarkRendererChat(1600, width)
				bottom := []string{"working", "▌ input"}
				if temperature == "warm" {
					r.DrawLog(chat, bottom, 1, 2)
					out.Reset()
				}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					frame := chat
					if temperature == "warm" {
						// Keep most of the storm stable while exercising the
						// renderer's row-diff path with a changing suffix.
						frame = append([]string(nil), chat...)
						frame[len(frame)-1] = fmt.Sprintf("tool output %d", n)
						r.DrawLog(frame, bottom, 1, 2)
					} else {
						cold := NewRenderer(&out)
						cold.Resize(width, 40)
						cold.DrawLog(frame, bottom, 1, 2)
					}
					out.Reset()
				}
			})
		}
	}
}

func BenchmarkRendererDrawLogToolStormCold(b *testing.B) {
	for _, width := range []int{48, 80, 160} {
		b.Run(fmt.Sprintf("width=%d", width), func(b *testing.B) {
			chat := benchmarkRendererChat(1600, width)
			bottom := []string{"working", "▌ input"}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				var out bytes.Buffer
				r := NewRenderer(&out)
				r.Resize(width, 40)
				r.DrawLog(chat, bottom, 1, 2)
			}
		})
	}
}

func benchmarkRendererChat(rows, width int) []string {
	chat := make([]string, rows)
	for i := range chat {
		chat[i] = fmt.Sprintf("│ tool-%03d result line with enough payload to exercise width truncation", i)
	}
	return chat
}
