package probe

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const maxEventBytes = 4 << 20

var errResponse = errors.New("response_error")

// ParseCompleted extracts the final text from a streamed Responses API
// response. It deliberately requires the response.completed event even when
// a text-bearing event was received earlier in the stream.
func ParseCompleted(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errResponse
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes)

	var result completedText
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		payload, ok := eventPayload(line)
		if !ok {
			continue
		}
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if err := result.add(payload); err != nil {
			return "", errResponse
		}
	}
	if scanner.Err() != nil || !result.completed {
		return "", errResponse
	}

	return result.text()
}

// eventPayload accepts normal SSE data fields and the one-response JSON form
// used by non-streaming-compatible host test doubles. Other SSE fields and
// comments are intentionally ignored.
func eventPayload(line string) (string, bool) {
	if strings.HasPrefix(line, "data:") {
		return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
	}
	if strings.HasPrefix(line, "{") {
		return line, true
	}
	return "", false
}

type completedText struct {
	completed bool

	terminalText    string
	hasTerminalText bool
	doneText        string
	hasDoneText     bool
	itemText        string
	hasItemText     bool
	deltas          strings.Builder
}

type responseEvent struct {
	Type     string          `json:"type"`
	Response json.RawMessage `json:"response"`
	Text     *string         `json:"text"`
	Delta    string          `json:"delta"`
	Item     json.RawMessage `json:"item"`
	Part     json.RawMessage `json:"part"`
}

func (c *completedText) add(payload string) error {
	var event responseEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return errResponse
	}

	switch event.Type {
	case "response.failed", "response.incomplete", "error":
		return errResponse
	case "response.completed":
		c.completed = true
		if text, ok := outputText(event.Response); ok {
			c.terminalText = text
			c.hasTerminalText = true
		}
	case "response.output_text.done":
		if event.Text != nil {
			c.doneText = *event.Text
			c.hasDoneText = true
		}
	case "response.output_item.done":
		if text, ok := outputText(event.Item); ok {
			c.itemText = text
			c.hasItemText = true
		}
	case "response.content_part.done":
		if text, ok := outputText(event.Part); ok {
			c.itemText = text
			c.hasItemText = true
		}
	case "response.output_text.delta":
		c.deltas.WriteString(event.Delta)
	}
	return nil
}

func (c completedText) text() (string, error) {
	switch {
	case c.hasTerminalText:
		return c.terminalText, nil
	case c.hasDoneText:
		return c.doneText, nil
	case c.hasItemText:
		return c.itemText, nil
	case c.deltas.Len() > 0:
		return c.deltas.String(), nil
	default:
		return "", errResponse
	}
}

// outputText extracts output text from a completed response, item, or
// content part. It ignores reasoning and other content types, and joins
// multiple output text parts in stream order.
func outputText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}

	if typ := rawString(value, "type"); typ == "output_text" {
		if text, ok := rawStringOK(value, "text"); ok {
			return text, true
		}
	}
	if text, ok := rawStringOK(value, "output_text"); ok {
		return text, true
	}

	var parts []string
	if content, ok := rawArray(value, "content"); ok {
		for _, part := range content {
			if text, found := outputText(part); found {
				parts = append(parts, text)
			}
		}
	}
	if output, ok := rawArray(value, "output"); ok {
		for _, item := range output {
			if text, found := outputText(item); found {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ""), true
	}
	return "", false
}

func rawString(object map[string]json.RawMessage, name string) string {
	value, _ := rawStringOK(object, name)
	return value
}

func rawStringOK(object map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := object[name]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func rawArray(object map[string]json.RawMessage, name string) ([]json.RawMessage, bool) {
	raw, ok := object[name]
	if !ok {
		return nil, false
	}
	var value []json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
