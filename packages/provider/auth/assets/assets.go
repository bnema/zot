// Package assets holds static resources embedded in the zut binary.
// Currently just the zut logo used by the tui welcome banner.
package assets

import _ "embed"

// LogoPNG is the pixel-art zut `z` logo as PNG bytes.
// Used by the interactive welcome banner; decoded once and rasterized
// to Unicode half-blocks so it renders on any terminal without needing
// inline image support.
//
//go:embed zut-logo.png
var LogoPNG []byte
