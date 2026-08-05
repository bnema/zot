package agent

import (
	_ "embed"
	"strings"
)

//go:embed ponytail.md
var ponytailSystemAddendum string

// PonytailSystemAddendum returns the compact coding-guidance block included
// in resolved system prompts when Ponytail mode is enabled.
func PonytailSystemAddendum() string {
	return strings.TrimSpace(ponytailSystemAddendum)
}
