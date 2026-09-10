# Serve native ES modules as embedded assets

The panel uses embedded native HTML, CSS, and JavaScript ES modules. Because CLIProxyAPI matches resource routes exactly and rejects wildcard declarations, plugin registration lists `/panel` as the sole menu entry and every `/panel/assets/...` file as an exact menu-less resource. This keeps feature code in focused modules without introducing a frontend framework or build-time package graph, and avoids the reference panel's fragile concatenation of global scripts whose later definitions override earlier behavior.
