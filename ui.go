package main

import (
	_ "embed"
	"strings"
)

//go:embed ui.html
var uiHTML string

// RenderAppHTML returns the full HTML page with the session token injected.
func RenderAppHTML(sessionToken string) string {
	return strings.ReplaceAll(uiHTML, "{{SESSION_TOKEN}}", sessionToken)
}
