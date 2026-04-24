package main

import (
	_ "embed"
	"strings"
)

//go:embed ui.html
var uiHTML string

//go:embed ui.css
var uiCSS string

//go:embed ui_core.js
var uiCoreJS string

//go:embed ui_results.js
var uiResultsJS string

//go:embed ui_actions.js
var uiActionsJS string

//go:embed ui_settings.js
var uiSettingsJS string

//go:embed ui_onboarding.js
var uiOnboardingJS string

//go:embed ui_editor.js
var uiEditorJS string

// RenderAppHTML returns the full HTML page with the session token injected.
func RenderAppHTML(sessionToken string) string {
	return strings.ReplaceAll(uiHTML, "{{SESSION_TOKEN}}", sessionToken)
}

// RenderUICoreJS returns the core UI script with the session token injected.
func RenderUICoreJS(sessionToken string) string {
	return strings.ReplaceAll(uiCoreJS, "{{SESSION_TOKEN}}", sessionToken)
}
