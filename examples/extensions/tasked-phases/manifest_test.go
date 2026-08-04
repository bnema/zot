package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestManifestMatchesExtensionConstants(t *testing.T) {
	data, err := os.ReadFile("extension.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name    string   `json:"name"`
		Version string   `json:"version"`
		Skills  []string `json:"skills"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("extension.json is not valid JSON: %v", err)
	}
	if manifest.Name != extensionName || manifest.Version != extensionVersion {
		t.Fatalf("manifest = %s/%s, constants = %s/%s", manifest.Name, manifest.Version, extensionName, extensionVersion)
	}
	for _, skill := range manifest.Skills {
		if _, err := os.Stat(filepath.Join(skill, "SKILL.md")); err != nil {
			t.Errorf("declared skill %q has no SKILL.md: %v", skill, err)
		}
	}
}

func TestToolSchemaEnumMatchesToolActions(t *testing.T) {
	var schema struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(toolSchema), &schema); err != nil {
		t.Fatalf("tool schema is not valid JSON: %v", err)
	}
	if !slices.Equal(schema.Properties.Action.Enum, toolActions) {
		t.Fatalf("schema enum = %v, toolActions = %v", schema.Properties.Action.Enum, toolActions)
	}
}
