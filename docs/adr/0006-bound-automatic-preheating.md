# Bound automatic preheating

Automatic preheating prioritizes Available Coverage during Critical Work Periods while penalizing Idle Window time. At a planned execution, the plugin skips accounts that already have a Sufficient Window, allows only one delayed Compensation Attempt after an eligible failure, and never converts failure into a Quota Reset. Disabled accounts cannot receive any Probe Request; accounts marked unavailable are paused for automatic preheating but may receive an explicitly authorized manual Health Probe without losing their schedule selection.
