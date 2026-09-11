package management

import (
	"io/fs"
	"strings"
)

var assetTypes = map[string]string{
	"/panel":                "text/html; charset=utf-8",
	"/styles.css":           "text/css; charset=utf-8",
	"/modules/api.js":       "text/javascript; charset=utf-8",
	"/modules/state.js":     "text/javascript; charset=utf-8",
	"/modules/accounts.js":  "text/javascript; charset=utf-8",
	"/modules/schedule.js":  "text/javascript; charset=utf-8",
	"/modules/simulator.js": "text/javascript; charset=utf-8",
	"/modules/history.js":   "text/javascript; charset=utf-8",
	"/modules/main.js":      "text/javascript; charset=utf-8",
	"/modules/dashboard.js": "text/javascript; charset=utf-8",
}

var assetPaths = []string{
	"/panel",
	"/styles.css",
	"/modules/api.js",
	"/modules/state.js",
	"/modules/accounts.js",
	"/modules/schedule.js",
	"/modules/simulator.js",
	"/modules/history.js",
	"/modules/main.js",
	"/modules/dashboard.js",
}

// Assets reads the fixed browser asset allowlist from an injected filesystem.
// The root package supplies an embed.FS in production; tests and host adapters
// can provide os.DirFS or another fs.FS without changing the router.
type Assets struct {
	FS fs.FS
}

func NewAssets(files fs.FS) Assets {
	return Assets{FS: files}
}

func (a Assets) Read(assetPath string) ([]byte, string, error) {
	assetPath = normalizeAssetPath(assetPath)
	contentType, ok := assetTypes[assetPath]
	if !ok {
		return nil, "", fs.ErrNotExist
	}
	if a.FS == nil {
		return nil, "", fs.ErrNotExist
	}
	name := strings.TrimPrefix(assetPath, "/")
	if assetPath == "/panel" {
		name = "panel.html"
	}
	candidates := []string{name, "web/" + name}
	var lastErr error
	for _, candidate := range candidates {
		body, err := fs.ReadFile(a.FS, candidate)
		if err == nil {
			return body, contentType, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fs.ErrNotExist
	}
	return nil, "", lastErr
}

func normalizeAssetPath(value string) string {
	if value == "" {
		return ""
	}
	if index := strings.IndexByte(value, '?'); index >= 0 {
		value = value[:index]
	}
	if index := strings.IndexByte(value, '#'); index >= 0 {
		value = value[:index]
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
}

// NormalizePluginID accepts only one path-safe runtime identifier. Invalid,
// empty, or ambiguous values intentionally collapse to the unsuffixed
// fallback used by the plugin's registration and browser bootstrap.
func NormalizePluginID(value string) string {
	if value == "" || strings.TrimSpace(value) != value || value == "." || value == ".." {
		return DefaultPluginID
	}
	if strings.ContainsAny(value, "/?\\\t\r\n") {
		return DefaultPluginID
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return DefaultPluginID
	}
	return value
}

// PluginIDFromResourceBasePath extracts the plugin identifier from the host
// registration value. The exact accepted field is intentionally strict so a
// malformed path can never create a second or traversal-like route prefix.
func PluginIDFromResourceBasePath(value string) string {
	const prefix = "/v0/resource/plugins/"
	if !strings.HasPrefix(value, prefix) {
		return DefaultPluginID
	}
	id := strings.TrimPrefix(value, prefix)
	if id == "" || strings.ContainsAny(id, "/?\\\t\r\n") {
		return DefaultPluginID
	}
	return NormalizePluginID(id)
}

// PluginIDFromResourceFields accepts the casing variants emitted by host ABI
// generations without making any of those field names part of route code.
func PluginIDFromResourceFields(fields map[string]string) string {
	for _, name := range []string{"ResourceBasePath", "resource_base_path", "resourceBasePath"} {
		if value, ok := fields[name]; ok {
			return PluginIDFromResourceBasePath(value)
		}
	}
	return DefaultPluginID
}

func Registration(pluginID string) RegistrationResult {
	id := NormalizePluginID(pluginID)
	prefix := "/plugins/" + id
	routes := []Route{
		{Method: "GET", Path: prefix + "/status"},
		{Method: "GET", Path: prefix + "/accounts"},
		{Method: "GET", Path: prefix + "/schedule"},
		{Method: "PUT", Path: prefix + "/schedule"},
		{Method: "POST", Path: prefix + "/simulate"},
		{Method: "POST", Path: prefix + "/probes"},
		{Method: "GET", Path: prefix + "/history"},
		{Method: "DELETE", Path: prefix + "/history"},
		{Method: "GET", Path: prefix + "/quota"},
		{Method: "POST", Path: prefix + "/quota/refresh"},
		{Method: "POST", Path: prefix + "/quota/reset"},
		{Method: "GET", Path: prefix + "/reset-audit"},
		{Method: "DELETE", Path: prefix + "/reset-audit"},
	}
	resources := make([]Resource, 0, len(assetPaths))
	for index, assetPath := range assetPaths {
		menu := ""
		if index == 0 {
			menu = "Codex Window Reset"
		}
		resources = append(resources, Resource{
			Path:        assetPath,
			ContentType: assetTypes[assetPath],
			Menu:        menu,
		})
	}
	return RegistrationResult{Routes: routes, Resources: resources}
}
