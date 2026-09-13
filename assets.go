package main

import (
	"embed"

	"github.com/tapaixx/codex-window-reset/internal/management"
)

// EmbeddedWebAssets is passed to the management package by the native host
// integration. Keeping the embed at the repository root lets Go include the
// sibling web directory without duplicating files under internal packages.
//
//go:embed web/panel.html web/styles.css web/simulator.css web/modules/api.js web/modules/dashboard.js web/modules/timeline.js web/modules/main.js
var EmbeddedWebAssets embed.FS

func embeddedManagementAssets() management.Assets {
	assets := management.NewAssets(EmbeddedWebAssets)
	assets.Version = pluginVersion
	return assets
}
