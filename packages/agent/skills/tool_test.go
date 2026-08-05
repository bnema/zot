package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/provider"
)

func TestToolLoadsSkillByPathAlias(t *testing.T) {
	tool := NewTool([]*Skill{{
		Name:    "golang-patterns",
		Aliases: []string{"systems-backend/subskills/golang-patterns"},
		Body:    "Go body",
	}})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"systems-backend/subskills/golang-patterns"}`), nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute() returned an error result: %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("Execute() content = %#v", result.Content)
	}
	text, ok := result.Content[0].(provider.TextBlock)
	if !ok {
		t.Fatalf("Execute() content type = %T", result.Content[0])
	}
	if !strings.Contains(text.Text, "Go body") {
		t.Fatalf("Execute() text = %q", text.Text)
	}
}
