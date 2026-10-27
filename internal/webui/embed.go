package webui

import "embed"

// The WebUI bundle is a build artifact and is not tracked in git. `all:`
// keeps the embed valid when dist/ only contains its .gitkeep placeholder;
// in that case the server answers 503 "WebUI is not built" instead of
// failing to compile.
//
//go:embed all:dist
var FS embed.FS
