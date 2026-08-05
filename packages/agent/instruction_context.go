package agent

import (
	"sort"

	"github.com/bnema/zut/packages/agent/extensions"
	"github.com/bnema/zut/packages/agent/skills"
)

// instructionContextPaths returns the loaded instruction paths in effective
// prompt order for display in interactive startup metadata.
func instructionContextPaths(files []ContextFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func startupExtensionNames(exts []*extensions.Extension) []string {
	names := make([]string, 0, len(exts))
	for _, ext := range exts {
		if ext == nil || !ext.Manifest.IsEnabled() || ext.Manifest.Name == "" {
			continue
		}
		names = append(names, ext.Manifest.Name)
	}
	sort.Strings(names)
	return names
}

func startupSkillNames(discovered []*skills.Skill) []string {
	visible := skills.VisibleSkills(discovered)
	names := make([]string, 0, len(visible))
	for _, skill := range visible {
		if skill.Name != "" {
			names = append(names, skill.Name)
		}
	}
	sort.Strings(names)
	return names
}
