package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ParseDiagnostics parses output from one of the built-in CLI linter formats.
// Unknown parser names use the generic file:line:column parser.
func ParseDiagnostics(parser, provider, cwd, output string) []Diagnostic {
	switch strings.ToLower(strings.TrimSpace(parser)) {
	case "eslint-json", "eslint":
		return parseESLint(provider, cwd, output)
	case "golangci-lint-json", "golangci", "golangci-json":
		return parseGolangCILint(provider, cwd, output)
	case "ruff-json", "ruff":
		return parseRuff(provider, cwd, output)
	case "sarif":
		return parseSARIF(provider, cwd, output)
	default:
		return parseGeneric(provider, cwd, output)
	}
}

func parseESLint(provider, cwd, output string) []Diagnostic {
	var results []struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			RuleID    string `json:"ruleId"`
			Severity  int    `json:"severity"`
			Message   string `json:"message"`
			Line      int    `json:"line"`
			Column    int    `json:"column"`
			EndLine   int    `json:"endLine"`
			EndColumn int    `json:"endColumn"`
		} `json:"messages"`
	}
	if json.Unmarshal([]byte(output), &results) != nil {
		return parseGeneric(provider, cwd, output)
	}
	var out []Diagnostic
	for _, result := range results {
		path := normalizeDiagnosticPath(cwd, result.FilePath)
		for _, message := range result.Messages {
			severity := "info"
			if message.Severity >= 2 {
				severity = "error"
			} else if message.Severity == 1 {
				severity = "warning"
			}
			out = append(out, Diagnostic{Path: path, Server: provider, Source: provider, Severity: severity, Code: message.RuleID, Message: message.Message, Range: oneBasedRange(message.Line, message.Column, message.EndLine, message.EndColumn)})
		}
	}
	return out
}

func parseGolangCILint(provider, cwd, output string) []Diagnostic {
	var root struct {
		Issues []struct {
			FromLinter string `json:"FromLinter"`
			Text       string `json:"Text"`
			Severity   string `json:"Severity"`
			Pos        struct {
				Filename string `json:"Filename"`
				Line     int    `json:"Line"`
				Column   int    `json:"Column"`
				Offset   int    `json:"Offset"`
			} `json:"Pos"`
			LineRange *struct {
				From int `json:"From"`
				To   int `json:"To"`
			} `json:"LineRange"`
		} `json:"Issues"`
	}
	if json.Unmarshal([]byte(output), &root) != nil {
		return parseGeneric(provider, cwd, output)
	}
	var out []Diagnostic
	for _, issue := range root.Issues {
		severity := normalizeSeverity(issue.Severity)
		endLine := issue.Pos.Line
		if issue.LineRange != nil && issue.LineRange.To > endLine {
			endLine = issue.LineRange.To
		}
		out = append(out, Diagnostic{Path: normalizeDiagnosticPath(cwd, issue.Pos.Filename), Server: provider, Source: issue.FromLinter, Severity: severity, Code: issue.FromLinter, Message: issue.Text, Range: oneBasedRange(issue.Pos.Line, issue.Pos.Column, endLine, issue.Pos.Column)})
	}
	return out
}

func parseRuff(provider, cwd, output string) []Diagnostic {
	var records []struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Filename string `json:"filename"`
		Location struct {
			Row    int `json:"row"`
			Column int `json:"column"`
		} `json:"location"`
		EndLocation struct {
			Row    int `json:"row"`
			Column int `json:"column"`
		} `json:"end_location"`
		Fix any `json:"fix"`
	}
	if json.Unmarshal([]byte(output), &records) != nil {
		return parseGeneric(provider, cwd, output)
	}
	var out []Diagnostic
	for _, record := range records {
		endLine, endColumn := record.EndLocation.Row, record.EndLocation.Column
		if endLine == 0 {
			endLine, endColumn = record.Location.Row, record.Location.Column
		}
		out = append(out, Diagnostic{Path: normalizeDiagnosticPath(cwd, record.Filename), Server: provider, Source: provider, Severity: "error", Code: record.Code, Message: record.Message, Range: oneBasedRange(record.Location.Row, record.Location.Column, endLine, endColumn)})
	}
	return out
}

func parseSARIF(provider, cwd, output string) []Diagnostic {
	var root struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					Physical struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine   int `json:"startLine"`
							StartColumn int `json:"startColumn"`
							EndLine     int `json:"endLine"`
							EndColumn   int `json:"endColumn"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if json.Unmarshal([]byte(output), &root) != nil {
		return parseGeneric(provider, cwd, output)
	}
	var out []Diagnostic
	for _, run := range root.Runs {
		for _, result := range run.Results {
			if len(result.Locations) == 0 {
				continue
			}
			location := result.Locations[0].Physical
			path := location.ArtifactLocation.URI
			if strings.HasPrefix(path, "file://") {
				if decoded, err := uriToPath(path); err == nil {
					path = decoded
				}
			}
			out = append(out, Diagnostic{Path: normalizeDiagnosticPath(cwd, path), Server: provider, Source: provider, Severity: normalizeSeverity(result.Level), Code: result.RuleID, Message: result.Message.Text, Range: oneBasedRange(location.Region.StartLine, location.Region.StartColumn, location.Region.EndLine, location.Region.EndColumn)})
		}
	}
	return out
}

var genericDiagnosticPattern = regexp.MustCompile(`^\s*(.+?):([0-9]+):([0-9]+)(?::\s*([A-Za-z]+))?\s*[-:]?\s*(.*)$`)

func parseGeneric(provider, cwd, output string) []Diagnostic {
	var out []Diagnostic
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := genericDiagnosticPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		lineNo, _ := strconv.Atoi(match[2])
		columnNo, _ := strconv.Atoi(match[3])
		message := strings.TrimSpace(match[5])
		if message == "" {
			message = strings.TrimSpace(match[4])
		}
		severity := normalizeSeverity(match[4])
		out = append(out, Diagnostic{Path: normalizeDiagnosticPath(cwd, match[1]), Server: provider, Source: provider, Severity: severity, Message: message, Range: oneBasedRange(lineNo, columnNo, lineNo, columnNo)})
	}
	return out
}

func oneBasedRange(line, column, endLine, endColumn int) Range {
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	if endLine < 1 {
		endLine = line
	}
	if endColumn < 1 {
		endColumn = column
	}
	return Range{Start: Position{Line: line - 1, Character: column - 1}, End: Position{Line: endLine - 1, Character: endColumn - 1}}
}

func normalizeSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error", "err", "fatal", "failure":
		return "error"
	case "warning", "warn":
		return "warning"
	case "hint":
		return "hint"
	default:
		return "info"
	}
}

func normalizeDiagnosticPath(cwd, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "file://") {
		if decoded, err := uriToPath(path); err == nil {
			path = decoded
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// FormatDiagnostic is the compact, model-facing form of one diagnostic.
func FormatDiagnostic(d Diagnostic, root string) string {
	path := d.Path
	if root != "" {
		if rel, err := filepath.Rel(root, d.Path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			path = filepath.ToSlash(rel)
		}
	}
	code := ""
	if d.Code != "" {
		code = " [" + d.Code + "]"
	}
	message := compactWhitespace(d.Message)
	if len(message) > 220 {
		message = message[:219] + "…"
	}
	return fmt.Sprintf("%s: %s:%d:%d%s %s", strings.ToUpper(defaultSeverity(d.Severity)), filepath.ToSlash(path), d.Range.Start.Line+1, d.Range.Start.Character+1, code, message)
}

func defaultSeverity(value string) string {
	if value == "" {
		return "info"
	}
	return value
}

func compactWhitespace(value string) string { return strings.Join(strings.Fields(value), " ") }
