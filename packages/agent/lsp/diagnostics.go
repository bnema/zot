package lsp

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const DefaultDiagnosticCap = 50

var missingModulePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)cannot find module ["']([^"']+)["']`),
	regexp.MustCompile(`(?i)could not find (?:a declaration file for )?module ["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(?:cannot|could not) resolve (?:module|import|package) ["']?([^"' ]+)["']?`),
	regexp.MustCompile(`(?i)import .*?(["'][^"']+["']).*could not be resolved`),
}

// DiagnosticKey is stable across providers. Server/source are intentionally
// excluded so an LSP and a CLI linter reporting the same issue collapse.
func DiagnosticKey(d Diagnostic) string {
	// Match the bridge's merge key: the start location is stable enough to
	// identify one report while providers may disagree on the end range.
	return strings.Join([]string{d.Path, defaultSeverity(d.Severity), d.Code,
		fmt.Sprintf("%d:%d", d.Range.Start.Line, d.Range.Start.Character), compactWhitespace(d.Message)}, "\x00")
}

func diagnosticIssueKey(d Diagnostic) string {
	return strings.Join([]string{defaultSeverity(d.Severity), d.Code, compactWhitespace(d.Message)}, "\x00")
}

// DiagnosticIdentity identifies the issue independently of its current
// location. It is used by write/edit diagnostics to avoid replaying the same
// error after a harmless line shift while still re-surfacing it after removal.
func DiagnosticIdentity(d Diagnostic) string {
	return diagnosticIssueKey(d)
}

// DeduplicateDiagnostics removes duplicates across servers and sorts errors,
// warnings, info, and hints in that order, then path and location.
func DeduplicateDiagnostics(input []Diagnostic) []Diagnostic {
	seen := make(map[string]Diagnostic, len(input))
	for _, diagnostic := range input {
		key := DiagnosticKey(diagnostic)
		if old, exists := seen[key]; !exists || old.Server == "" {
			seen[key] = diagnostic
		}
	}
	out := make([]Diagnostic, 0, len(seen))
	for _, diagnostic := range seen {
		out = append(out, diagnostic)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if severityRank(out[i].Severity) != severityRank(out[j].Severity) {
			return severityRank(out[i].Severity) < severityRank(out[j].Severity)
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Range.Start.Line != out[j].Range.Start.Line {
			return out[i].Range.Start.Line < out[j].Range.Start.Line
		}
		if out[i].Range.Start.Character != out[j].Range.Start.Character {
			return out[i].Range.Start.Character < out[j].Range.Start.Character
		}
		if out[i].Message != out[j].Message {
			return out[i].Message < out[j].Message
		}
		return out[i].Code < out[j].Code
	})
	return out
}

func severityRank(value string) int {
	switch strings.ToLower(value) {
	case "error":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	case "hint":
		return 3
	default:
		return 2
	}
}

// SummarizeDiagnostics renders a bounded diagnostic summary. Repeated issues
// are represented once with occurrence/file counts; module-resolution errors
// retain their root issue while grouping the cascade of secondary errors.
func SummarizeDiagnostics(input []Diagnostic, root string, cap int) string {
	if cap <= 0 {
		cap = DefaultDiagnosticCap
	}
	diagnostics := DeduplicateDiagnostics(input)
	if len(diagnostics) == 0 {
		return "No diagnostics."
	}
	counts := [4]int{}
	for _, diagnostic := range diagnostics {
		switch severityRank(diagnostic.Severity) {
		case 0:
			counts[0]++
		case 1:
			counts[1]++
		case 2:
			counts[2]++
		case 3:
			counts[3]++
		}
	}
	parts := make([]string, 0, 4)
	for i, label := range []string{"error", "warning", "info", "hint"} {
		if counts[i] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s%s", counts[i], label, plural(counts[i])))
		}
	}
	lines := []string{fmt.Sprintf("Diagnostics: %s (deduplicated %d)", strings.Join(parts, ", "), len(diagnostics))}
	secondary := moduleSecondary(diagnostics)
	groups := make(map[string][]Diagnostic)
	for _, diagnostic := range diagnostics {
		if secondary[DiagnosticKey(diagnostic)] {
			continue
		}
		key := diagnosticIssueKey(diagnostic)
		groups[key] = append(groups[key], diagnostic)
	}
	emitted := make(map[string]bool)
	emittedSecondary := make(map[string]bool)
	displayed := 0
	for _, diagnostic := range diagnostics {
		if displayed >= cap {
			break
		}
		if secondary[DiagnosticKey(diagnostic)] {
			continue
		}
		groupKey := diagnosticIssueKey(diagnostic)
		bucket := groups[groupKey]
		if len(bucket) >= 3 || uniquePaths(bucket) >= 2 {
			if emitted[groupKey] {
				continue
			}
			emitted[groupKey] = true
			lines = append(lines, formatRepeated(bucket, root))
			displayed += len(bucket)
		} else {
			lines = append(lines, "- "+FormatDiagnostic(diagnostic, root))
			displayed++
		}
		if displayed < cap && missingModuleName(diagnostic) != "" && !emittedSecondary[diagnostic.Path] {
			if secondaryLine := formatSecondaryForFile(diagnostic.Path, diagnostics, secondary, root); secondaryLine != "" {
				lines = append(lines, "- "+secondaryLine)
				emittedSecondary[diagnostic.Path] = true
				displayed++
			}
		}
	}
	omitted := len(diagnostics) - minInt(displayed, len(diagnostics))
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("... truncated; %d diagnostics omitted (hard cap %d)", omitted, cap))
	}
	return strings.Join(lines, "\n")
}

func formatRepeated(bucket []Diagnostic, root string) string {
	paths := make(map[string]bool)
	lines := make(map[int]bool)
	for _, diagnostic := range bucket {
		paths[diagnostic.Path] = true
		lines[diagnostic.Range.Start.Line+1] = true
	}
	example := make([]string, 0, 3)
	for _, diagnostic := range bucket {
		path := diagnostic.Path
		if root != "" {
			if rel, err := filepathRel(root, path); err == nil {
				path = rel
			}
		}
		if !contains(example, path) && len(example) < 3 {
			example = append(example, path)
		}
	}
	extra := ""
	if len(example) > 0 {
		extra = fmt.Sprintf(" (examples: %s)", strings.Join(example, ", "))
	}
	code := ""
	if bucket[0].Code != "" {
		code = " [" + bucket[0].Code + "]"
	}
	where := fmt.Sprintf("%d occurrences across %d files", len(bucket), len(paths))
	if len(paths) == 1 {
		where = fmt.Sprintf("%d occurrences in %s", len(bucket), example[0])
	}
	if len(paths) == 1 && len(lines) > 0 {
		where += fmt.Sprintf(" (lines: %s)", joinInts(lines, 3))
	}
	message := truncateText(compactWhitespace(bucket[0].Message), 220)
	return fmt.Sprintf("%s: repeated %s%s %s%s", strings.ToUpper(defaultSeverity(bucket[0].Severity)), where, code, message, extra)
}

func moduleSecondary(diagnostics []Diagnostic) map[string]bool {
	roots := make(map[string]bool)
	byFile := make(map[string][]Diagnostic)
	for _, diagnostic := range diagnostics {
		byFile[diagnostic.Path] = append(byFile[diagnostic.Path], diagnostic)
		if missingModuleName(diagnostic) != "" {
			roots[diagnostic.Path] = true
		}
	}
	secondary := make(map[string]bool)
	for path, fileDiagnostics := range byFile {
		if !roots[path] {
			continue
		}
		count := 0
		for _, diagnostic := range fileDiagnostics {
			if missingModuleName(diagnostic) == "" {
				count++
			}
		}
		if count < 2 {
			continue
		}
		for _, diagnostic := range fileDiagnostics {
			if missingModuleName(diagnostic) == "" {
				secondary[DiagnosticKey(diagnostic)] = true
			}
		}
	}
	return secondary
}

func formatSecondaryForFile(path string, diagnostics []Diagnostic, secondary map[string]bool, root string) string {
	count := 0
	modules := make(map[string]bool)
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path && secondary[DiagnosticKey(diagnostic)] {
			count++
		}
		if diagnostic.Path == path && missingModuleName(diagnostic) != "" {
			modules[missingModuleName(diagnostic)] = true
		}
	}
	if count == 0 {
		return ""
	}
	shownPath := path
	if root != "" {
		if rel, err := filepathRel(root, path); err == nil {
			shownPath = rel
		}
	}
	moduleNames := make([]string, 0, len(modules))
	for name := range modules {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)
	return fmt.Sprintf("SECONDARY: grouped %d additional diagnostics in %s under module-resolution error (%s). Fix the root import error first, then re-check.", count, shownPath, strings.Join(moduleNames, ", "))
}

func missingModuleName(diagnostic Diagnostic) string {
	if strings.ToLower(diagnostic.Severity) != "error" {
		return ""
	}
	for _, pattern := range missingModulePatterns {
		if match := pattern.FindStringSubmatch(diagnostic.Message); len(match) > 1 {
			return strings.Trim(match[1], `"'`)
		}
	}
	return ""
}

func uniquePaths(diagnostics []Diagnostic) int {
	paths := make(map[string]bool)
	for _, diagnostic := range diagnostics {
		paths[diagnostic.Path] = true
	}
	return len(paths)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func joinInts(values map[int]bool, max int) string {
	ordered := make([]int, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Ints(ordered)
	if len(ordered) > max {
		ordered = ordered[:max]
	}
	parts := make([]string, len(ordered))
	for i, value := range ordered {
		parts[i] = fmt.Sprint(value)
	}
	return strings.Join(parts, ", ")
}
func filepathRel(root, path string) (string, error) {
	// Kept here rather than importing agent/tools: lsp is intentionally
	// independent of the tool package.
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path, nil
	}
	return filepath.ToSlash(rel), nil
}
