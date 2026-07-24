package agent

// instructionContextPaths returns the loaded instruction paths in effective
// prompt order for display in interactive startup metadata.
func instructionContextPaths(files []ContextFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}
