# Preserve local work calendar time

The Operator maintains the Work Calendar in a selected IANA timezone, defaulting to `Asia/Shanghai`, and its clock times continue to mean local wall time when offset rules change. The scheduler resolves each occurrence to a UTC instant immediately before scheduling and deduplicates by local occurrence identity, rather than freezing configuration as UTC and allowing local work periods to drift across daylight-saving changes.
