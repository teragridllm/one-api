package anthropic

import "testing"

func TestConvertInboundRequestWhenToolUse(t *testing.T) {
	request := InboundRequest{
		Model:     "ark-code-latest-claude",
		MaxTokens: 128,
		System: []any{
			map[string]any{"type": "text", "text": "You are concise."},
		},
		Messages: []InboundMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "List files"},
				},
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "ls",
						"input": map[string]any{"path": "."},
					},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_1",
						"content":     "main.go",
					},
				},
			},
		},
		Tools: []InboundTool{{
			Name:        "ls",
			Description: "List files",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
		ToolChoice: map[string]any{"type": "tool", "name": "ls"},
	}

	converted, err := ConvertInboundRequest(request)
	if err != nil {
		t.Fatalf("ConvertInboundRequest returned error: %v", err)
	}

	if converted.Model != "ark-code-latest-claude" {
		t.Fatalf("model = %q, want %q", converted.Model, "ark-code-latest-claude")
	}
	if len(converted.Messages) != 4 {
		t.Fatalf("messages length = %d, want 4", len(converted.Messages))
	}
	if converted.Messages[0].Role != "system" || converted.Messages[0].Content != "You are concise." {
		t.Fatalf("system message = %#v", converted.Messages[0])
	}
	if len(converted.Messages[2].ToolCalls) != 1 {
		t.Fatalf("tool calls length = %d, want 1", len(converted.Messages[2].ToolCalls))
	}
	if converted.Messages[2].ToolCalls[0].Function.Arguments != `{"path":"."}` {
		t.Fatalf("tool arguments = %q, want %q", converted.Messages[2].ToolCalls[0].Function.Arguments, `{"path":"."}`)
	}
	if converted.Messages[3].Role != "tool" || converted.Messages[3].ToolCallId != "toolu_1" {
		t.Fatalf("tool result message = %#v", converted.Messages[3])
	}
	if len(converted.Tools) != 1 || converted.Tools[0].Function.Name != "ls" {
		t.Fatalf("tools = %#v", converted.Tools)
	}
}
