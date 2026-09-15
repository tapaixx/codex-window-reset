package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

var errInvalidUsage = errors.New("invalid usage payload")

type parsedWindow struct {
	window         domain.UsageWindow
	durationSecond float64
	order          int
}

type windowCandidate struct {
	value          map[string]any
	name           string
	fallbackMinute int
	order          int
}

type resetCreditInfo struct {
	availableCount           *int
	applicableAvailableCount *int
	credits                  []domain.ResetCredit
	valid                    bool
	creditsPresent           bool
}

// ParseUsage converts one usage response into the domain snapshot shape. The
// parser accepts both casing conventions used by host/upstream versions and
// deliberately returns only sanitized parse errors.
func ParseUsage(raw []byte, capturedAt time.Time) (domain.UsageSnapshot, error) {
	root, ok := decodeObject(raw)
	if !ok {
		return domain.UsageSnapshot{}, errInvalidUsage
	}
	capturedAt = capturedAt.UTC()

	rate := firstObject(root, "rate_limit", "rateLimit", "rate_limits", "rateLimits")
	if rate == nil {
		rate = root
	}
	candidates := collectWindowCandidates(rate)
	parsed := make([]parsedWindow, 0, len(candidates))
	limitReached := boolValue(firstValue(rate, "limit_reached", "limitReached"))
	if boolValue(firstValue(rate, "allowed")) == false && hasValue(rate, "allowed") {
		limitReached = true
	}
	for _, candidate := range candidates {
		window, seconds, ok := parseWindow(candidate, capturedAt, limitReached)
		if !ok {
			return domain.UsageSnapshot{}, errInvalidUsage
		}
		parsed = append(parsed, parsedWindow{window: window, durationSecond: seconds, order: candidate.order})
	}
	if len(parsed) == 0 {
		return domain.UsageSnapshot{}, errInvalidUsage
	}
	sort.SliceStable(parsed, func(i, j int) bool {
		if parsed[i].durationSecond != parsed[j].durationSecond {
			return parsed[i].durationSecond < parsed[j].durationSecond
		}
		return parsed[i].order < parsed[j].order
	})
	windows := make([]domain.UsageWindow, len(parsed))
	for index, item := range parsed {
		item.window.Short = index == 0
		windows[index] = item.window
	}

	snapshot := domain.UsageSnapshot{
		CapturedAt:        capturedAt,
		Windows:           windows,
		LimitReached:      limitReached,
		ResetInfoComplete: false,
	}
	if value, ok := firstAny(root, "rate_limit_reached_type", "rateLimitReachedType"); ok {
		snapshot.ReachedType = stringValue(value)
	}
	snapshot.AvailableAt = earliestModelAvailability(root)
	if embedded, ok := firstAny(root, "rate_limit_reset_credits", "rateLimitResetCredits"); ok {
		info := parseResetCreditValue(embedded)
		if info.valid {
			snapshot.ResetCredits = cloneCredits(info.credits)
			snapshot.ResetApplicableCount = resetApplicableCount(info)
		}
	}
	return snapshot, nil
}

func decodeObject(raw []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok && object != nil
}

func collectWindowCandidates(rate map[string]any) []windowCandidate {
	if rate == nil {
		return nil
	}
	var result []windowCandidate
	seenNamed := make(map[string]struct{})
	order := 0
	for _, named := range []struct {
		name           string
		aliases        []string
		fallbackMinute int
	}{
		{name: "primary_window", aliases: []string{"primary_window", "primaryWindow", "primary"}, fallbackMinute: 300},
		{name: "secondary_window", aliases: []string{"secondary_window", "secondaryWindow", "secondary"}, fallbackMinute: 10080},
	} {
		if value, ok := firstObjectWithName(rate, named.aliases...); ok {
			result = append(result, windowCandidate{value: value, name: named.name, fallbackMinute: named.fallbackMinute, order: order})
			order++
			for _, alias := range named.aliases {
				seenNamed[alias] = struct{}{}
			}
		}
	}

	for _, name := range []string{"windows", "rate_limit_windows", "rateLimitWindows"} {
		value, ok := firstAny(rate, name)
		if !ok {
			continue
		}
		for _, object := range objectList(value) {
			result = append(result, windowCandidate{value: object, name: name, order: order})
			order++
		}
		break
	}

	keys := make([]string, 0, len(rate))
	for key := range rate {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, alreadyNamed := seenNamed[key]; alreadyNamed {
			continue
		}
		if key == "windows" || key == "rate_limit_windows" || key == "rateLimitWindows" {
			continue
		}
		object, ok := rate[key].(map[string]any)
		if !ok || !looksLikeWindow(object) {
			continue
		}
		result = append(result, windowCandidate{value: object, name: key, order: order})
		order++
	}
	return result
}

func firstObjectWithName(object map[string]any, names ...string) (map[string]any, bool) {
	for _, name := range names {
		value, ok := object[name].(map[string]any)
		if ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func objectList(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok && object != nil {
				result = append(result, object)
			}
		}
		return result
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			if object, ok := typed[key].(map[string]any); ok && object != nil {
				result = append(result, object)
			}
		}
		return result
	default:
		return nil
	}
}

func looksLikeWindow(object map[string]any) bool {
	return hasValue(object, "limit_window_seconds", "limitWindowSeconds", "window_minutes", "windowMinutes")
}

func parseWindow(candidate windowCandidate, capturedAt time.Time, limitReached bool) (domain.UsageWindow, float64, bool) {
	seconds, hasSeconds := numberValue(firstValue(candidate.value, "limit_window_seconds", "limitWindowSeconds"))
	if !hasSeconds {
		minutes, hasMinutes := numberValue(firstValue(candidate.value, "window_minutes", "windowMinutes"))
		if hasMinutes && minutes > 0 && finite(minutes) {
			seconds = minutes * 60
			hasSeconds = true
		}
	}
	if !hasSeconds && candidate.fallbackMinute > 0 && (candidate.name == "primary_window" || candidate.name == "secondary_window") {
		seconds = float64(candidate.fallbackMinute * 60)
		hasSeconds = true
	}
	if !hasSeconds || !finite(seconds) || seconds <= 0 {
		return domain.UsageWindow{}, 0, false
	}

	used, hasUsed := usagePercent(candidate.value)
	resetAt := resetAtFromWindow(candidate.value, capturedAt)
	if !hasUsed && limitReached && !resetAt.IsZero() {
		if hasValue(candidate.value, "used_percent", "usedPercent", "used", "used_fraction", "usedFraction", "remaining_percent", "remainingPercent") {
			return domain.UsageWindow{}, 0, false
		}
		used, hasUsed = 100, true
	}
	if !hasUsed || !finite(used) {
		return domain.UsageWindow{}, 0, false
	}
	minutes := int(math.Round(seconds / 60))
	if minutes < 1 {
		minutes = 1
	}
	return domain.UsageWindow{
		DurationMinutes:  minutes,
		RemainingPercent: int(math.Round(100 - used)),
		ResetAt:          resetAt,
	}, seconds, true
}

// usagePercent reads the unit from the field name rather than guessing it from
// the value.
//
// The previous rule scaled any value in [0,1] by 100 on the theory that it must
// be a fraction. That cannot tell one percent from a full window: a live
// response carrying used_percent 1 was read as 100% consumed, so an account
// with 99% of its window left was reported as having none. The same misreading
// on a long window puts remaining at 0, which is at or below the guardrail
// floor and holds the account out of every preheat.
func usagePercent(object map[string]any) (float64, bool) {
	if value, ok := firstAny(object, "used_percent", "usedPercent", "used"); ok {
		number, valid := numberValue(value)
		if !valid || !finite(number) || number < 0 || number > 100 {
			return 0, false
		}
		return number, true
	}
	if value, ok := firstAny(object, "used_fraction", "usedFraction"); ok {
		number, valid := numberValue(value)
		if !valid || !finite(number) || number < 0 || number > 1 {
			return 0, false
		}
		return number * 100, true
	}
	if value, ok := firstAny(object, "remaining_percent", "remainingPercent"); ok {
		remaining, valid := numberValue(value)
		if !valid || !finite(remaining) || remaining < 0 || remaining > 100 {
			return 0, false
		}
		return 100 - remaining, true
	}
	if value, ok := firstAny(object, "remaining_fraction", "remainingFraction"); ok {
		remaining, valid := numberValue(value)
		if !valid || !finite(remaining) || remaining < 0 || remaining > 1 {
			return 0, false
		}
		return 100 - remaining*100, true
	}
	return 0, false
}

func resetAtFromWindow(window map[string]any, capturedAt time.Time) time.Time {
	if value, ok := firstAny(window, "reset_at", "resetAt"); ok {
		if parsed := timeValue(value); !parsed.IsZero() {
			return parsed
		}
	}
	if value, ok := firstAny(window, "reset_after_seconds", "resetAfterSeconds"); ok {
		if seconds, valid := numberValue(value); valid && finite(seconds) && seconds >= 0 {
			return capturedAt.Add(time.Duration(seconds * float64(time.Second))).UTC()
		}
	}
	return time.Time{}
}

func timeValue(value any) time.Time {
	if value == nil {
		return time.Time{}
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return time.Time{}
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC()
		}
		value = text
	}
	number, valid := numberValue(value)
	if !valid || !finite(number) || number <= 0 {
		return time.Time{}
	}
	if number > 1e12 {
		return time.UnixMilli(int64(number)).UTC()
	}
	return time.Unix(int64(number), 0).UTC()
}

func parseResetCreditValue(value any) resetCreditInfo {
	if object, ok := value.(map[string]any); ok {
		if nested := firstObject(object, "rate_limit_reset_credits", "rateLimitResetCredits", "data"); nested != nil && !hasResetShape(object) {
			return parseResetCreditValue(nested)
		}
		available, availablePresent, valid := resetCountField(object, "available", "available_count", "availableCount")
		if !valid {
			return resetCreditInfo{}
		}
		applicable, applicablePresent, valid := resetCountField(object, "applicable", "applicable_available", "applicable_available_count", "applicableAvailable", "applicableAvailableCount")
		if !valid {
			return resetCreditInfo{}
		}
		credits, creditsPresent, valid := resetCreditListField(object, "credits", "reset_credits", "resetCredits")
		if !valid {
			return resetCreditInfo{}
		}
		if !availablePresent && !applicablePresent && !creditsPresent {
			return resetCreditInfo{}
		}
		info := resetCreditInfo{
			availableCount:           available,
			applicableAvailableCount: applicable,
			credits:                  credits,
			valid:                    true,
			creditsPresent:           creditsPresent,
		}
		sort.SliceStable(info.credits, func(i, j int) bool {
			return info.credits[i].ExpiresAt.Before(info.credits[j].ExpiresAt)
		})
		return info
	}
	if list, ok := value.([]any); ok {
		credits, valid := parseCreditListValue(list)
		if !valid {
			return resetCreditInfo{}
		}
		return resetCreditInfo{valid: true, credits: credits, creditsPresent: true}
	}
	return resetCreditInfo{}
}

func resetCountField(object map[string]any, names ...string) (*int, bool, bool) {
	var result *int
	present := false
	for _, name := range names {
		value, ok := object[name]
		if !ok {
			continue
		}
		present = true
		count, valid := resetCountValue(value)
		if !valid {
			return nil, present, false
		}
		if result == nil {
			result = &count
		}
	}
	return result, present, true
}

func resetCountValue(value any) (int, bool) {
	if _, stringValue := value.(string); stringValue {
		return 0, false
	}
	number, ok := numberValue(value)
	if !ok || !finite(number) || number < 0 || math.Trunc(number) != number {
		return 0, false
	}
	// Leave a small safety margin at the platform int limit so conversion can
	// never wrap a malformed upstream count.
	if number >= float64(^uint(0)>>1) {
		return 0, false
	}
	return int(number), true
}

func resetCreditListField(object map[string]any, names ...string) ([]domain.ResetCredit, bool, bool) {
	var result []domain.ResetCredit
	present := false
	for _, name := range names {
		value, ok := object[name]
		if !ok {
			continue
		}
		credits, valid := parseCreditListValue(value)
		if !valid {
			return nil, true, false
		}
		if !present {
			result = credits
		}
		present = true
	}
	return result, present, true
}

func parseCreditListValue(value any) ([]domain.ResetCredit, bool) {
	list, ok := value.([]any)
	if !ok {
		return nil, false
	}
	for _, item := range list {
		object, ok := item.(map[string]any)
		if !ok || object == nil || !validCreditObject(object) {
			return nil, false
		}
	}
	return parseCredits(list), true
}

func validCreditObject(object map[string]any) bool {
	if !validOptionalStringFields(object, "id", "ID", "reset_type", "resetType", "status") {
		return false
	}
	foundExpiry := false
	for _, name := range []string{"expires_at", "expiresAt"} {
		value, ok := object[name]
		if !ok {
			continue
		}
		foundExpiry = true
		if timeValue(value).IsZero() {
			return false
		}
	}
	return foundExpiry
}

func validOptionalStringFields(object map[string]any, names ...string) bool {
	for _, name := range names {
		if value, ok := object[name]; ok {
			if _, valid := value.(string); !valid {
				return false
			}
		}
	}
	return true
}

func hasResetShape(object map[string]any) bool {
	return hasValue(object,
		"available", "available_count", "availableCount",
		"applicable", "applicable_available", "applicable_available_count", "applicableAvailable", "applicableAvailableCount",
		"credits", "reset_credits", "resetCredits")
}

func parseCredits(list []any) []domain.ResetCredit {
	credits := make([]domain.ResetCredit, 0, len(list))
	for _, item := range list {
		object, ok := item.(map[string]any)
		if !ok || object == nil {
			continue
		}
		resetType := strings.TrimSpace(stringValue(firstValue(object, "reset_type", "resetType")))
		status := strings.TrimSpace(stringValue(firstValue(object, "status")))
		if resetType != "" && resetType != "codex_rate_limits" {
			continue
		}
		if status != "" && status != "available" {
			continue
		}
		expiresAt, ok := firstAny(object, "expires_at", "expiresAt")
		if !ok {
			continue
		}
		parsedExpiry := timeValue(expiresAt)
		if parsedExpiry.IsZero() {
			continue
		}
		credits = append(credits, domain.ResetCredit{
			ID:        stringValue(firstValue(object, "id", "ID")),
			ExpiresAt: parsedExpiry,
		})
	}
	return credits
}

func resetApplicableCount(info resetCreditInfo) *int {
	if info.applicableAvailableCount != nil {
		return cloneInt(info.applicableAvailableCount)
	}
	if info.availableCount != nil {
		return cloneInt(info.availableCount)
	}
	if len(info.credits) > 0 {
		count := len(info.credits)
		return &count
	}
	return nil
}

func firstObject(object map[string]any, names ...string) map[string]any {
	for _, name := range names {
		if value, ok := object[name].(map[string]any); ok && value != nil {
			return value
		}
	}
	return nil
}

func firstAny(object map[string]any, names ...string) (any, bool) {
	for _, name := range names {
		if value, ok := object[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func firstValue(object map[string]any, names ...string) any {
	value, _ := firstAny(object, names...)
	return value
}

func hasValue(object map[string]any, names ...string) bool {
	_, ok := firstAny(object, names...)
	return ok
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true") || strings.TrimSpace(typed) == "1"
	default:
		return false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func cloneCredits(values []domain.ResetCredit) []domain.ResetCredit {
	if values == nil {
		return nil
	}
	return append([]domain.ResetCredit(nil), values...)
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// earliestModelAvailability reads model_usage for the soonest time an
// unavailable model comes back. Upstream reports this separately from the
// usage percentages, and it is the only field that states when an account that
// cannot be used right now becomes usable again.
func earliestModelAvailability(root map[string]any) time.Time {
	usage, ok := firstAny(root, "model_usage", "modelUsage")
	if !ok {
		return time.Time{}
	}
	object, ok := usage.(map[string]any)
	if !ok {
		return time.Time{}
	}
	var soonest time.Time
	for _, value := range object {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if available, ok := firstAny(entry, "available"); ok && boolValue(available) {
			continue
		}
		at, ok := firstAny(entry, "available_at", "availableAt")
		if !ok {
			continue
		}
		parsed := timeValue(at)
		if parsed.IsZero() {
			continue
		}
		if soonest.IsZero() || parsed.Before(soonest) {
			soonest = parsed.UTC()
		}
	}
	return soonest
}
