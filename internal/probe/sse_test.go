package probe

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseCompleted(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "completed terminal",
			input: `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"OK"}]}]}}` + "\n\n",
			want:  "OK",
		},
		{
			name:  "done text",
			input: "data: {\"type\":\"response.output_text.done\",\"text\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n",
			want:  "OK",
		},
		{
			name:  "deltas",
			input: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"O\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"K\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n",
			want:  "OK",
		},
		{
			name:  "completed item",
			input: "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}}\n\ndata: {\"type\":\"response.completed\"}\n",
			want:  "OK",
		},
		{
			name:  "completed content part",
			input: "data: {\"type\":\"response.content_part.done\",\"part\":{\"type\":\"output_text\",\"text\":\"OK\"}}\n\ndata: {\"type\":\"response.completed\"}\n",
			want:  "OK",
		},
		{
			name:  "single JSON response",
			input: `{"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"OK"}]}]}}`,
			want:  "OK",
		},
		{
			name:    "arbitrary output_text field is rejected",
			input:   `data: {"type":"response.completed","response":{"output_text":"OK"}}` + "\n",
			wantErr: true,
		},
		{
			name:    "null output text is rejected",
			input:   `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":null}]}]}}` + "\n",
			wantErr: true,
		},
		{
			name:  "non data lines are ignored",
			input: "event: response.completed\n: comment\n\n" + `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"OK"}]}]}}` + "\n",
			want:  "OK",
		},
		{
			name:    "done only",
			input:   "data: [DONE]\n\n",
			wantErr: true,
		},
		{
			name:    "failure before completion",
			input:   "data: {\"type\":\"response.failed\"}\n\ndata: {\"type\":\"response.completed\"}\n",
			wantErr: true,
		},
		{
			name:    "incomplete",
			input:   "data: {\"type\":\"response.incomplete\"}\n",
			wantErr: true,
		},
		{
			name:    "error",
			input:   "data: {\"type\":\"error\",\"message\":\"bad\"}\n",
			wantErr: true,
		},
		{
			name:    "malformed data",
			input:   "data: {\"type\":\"response.completed\"\n",
			wantErr: true,
		},
		{
			name:    "done text without completion",
			input:   "data: {\"type\":\"response.output_text.done\",\"text\":\"OK\"}\n",
			wantErr: true,
		},
		{
			name:    "failure after completion",
			input:   "data: {\"type\":\"response.completed\",\"response\":{}}\n\ndata: {\"type\":\"error\"}\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCompleted(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %q, %v; want %q, error=%t", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestParseCompletedRejectsOversizedEvent(t *testing.T) {
	input := "data: " + strings.Repeat("x", (4<<20)+1) + "\n"
	got, err := ParseCompleted(strings.NewReader(input))
	if err == nil {
		t.Fatalf("ParseCompleted(%d-byte event) returned %q without an error", len(input), got)
	}
	if strings.Contains(err.Error(), "panic") {
		t.Fatalf("oversized event produced panic-like error: %v", err)
	}
}

func ExampleParseCompleted() {
	text, err := ParseCompleted(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n" + "data: {\"type\":\"response.completed\"}\n"))
	fmt.Println(text, err == nil)
	// Output: OK true
}
