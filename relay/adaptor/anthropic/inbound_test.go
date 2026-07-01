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

func TestConvertInboundRequestRoundTripPreservesAssistantToolUse(t *testing.T) {
	request := InboundRequest{
		Model:     "ark-code-latest-claude",
		MaxTokens: 128,
		Messages: []InboundMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "Read main.go"},
				},
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "Read",
						"input": map[string]any{"file_path": "main.go"},
					},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_1",
						"content":     "package main",
					},
				},
			},
		},
	}

	openAIRequest, err := ConvertInboundRequest(request)
	if err != nil {
		t.Fatalf("ConvertInboundRequest returned error: %v", err)
	}

	roundTripped := ConvertRequest(*openAIRequest)
	if len(roundTripped.Messages) != 3 {
		t.Fatalf("messages length = %d, want 3", len(roundTripped.Messages))
	}
	assistantMessage := roundTripped.Messages[1]
	if assistantMessage.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistantMessage.Role)
	}
	if len(assistantMessage.Content) != 1 {
		t.Fatalf("assistant content length = %d, want 1: %#v", len(assistantMessage.Content), assistantMessage.Content)
	}
	toolUse := assistantMessage.Content[0]
	if toolUse.Type != "tool_use" || toolUse.Id != "toolu_1" || toolUse.Name != "Read" {
		t.Fatalf("tool use content = %#v", toolUse)
	}
	input, ok := toolUse.Input.(map[string]any)
	if !ok {
		t.Fatalf("tool use input type = %T, want map[string]any", toolUse.Input)
	}
	if input["file_path"] != "main.go" {
		t.Fatalf("tool use input = %#v", input)
	}
}

func TestConvertInboundRequestRoundTripMergesParallelToolResults(t *testing.T) {
	request := InboundRequest{
		Model:     "ark-code-latest-claude",
		MaxTokens: 128,
		Messages: []InboundMessage{
			{
				Role:    "user",
				Content: "Create the install tasks.",
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "call_1",
						"name":  "TaskCreate",
						"input": map[string]any{"subject": "安装 Ultimate Edition (OpenCode)"},
					},
					map[string]any{
						"type":  "tool_use",
						"id":    "call_2",
						"name":  "TaskCreate",
						"input": map[string]any{"subject": "安装 Light Edition (Codex CLI)"},
					},
					map[string]any{
						"type":  "tool_use",
						"id":    "call_3",
						"name":  "TaskCreate",
						"input": map[string]any{"subject": "验证安装"},
					},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "call_1",
						"content":     "Task #1 created successfully",
					},
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "call_2",
						"content":     "Task #2 created successfully",
					},
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "call_3",
						"content":     "Task #3 created successfully",
					},
				},
			},
		},
	}

	openAIRequest, err := ConvertInboundRequest(request)
	if err != nil {
		t.Fatalf("ConvertInboundRequest returned error: %v", err)
	}

	roundTripped := ConvertRequest(*openAIRequest)
	if len(roundTripped.Messages) != 3 {
		t.Fatalf("messages length = %d, want 3: %#v", len(roundTripped.Messages), roundTripped.Messages)
	}
	toolResults := roundTripped.Messages[2]
	if toolResults.Role != "user" {
		t.Fatalf("tool results role = %q, want user", toolResults.Role)
	}
	if len(toolResults.Content) != 3 {
		t.Fatalf("tool result content length = %d, want 3: %#v", len(toolResults.Content), toolResults.Content)
	}
	for i, content := range toolResults.Content {
		if content.Type != "tool_result" {
			t.Fatalf("content[%d].Type = %q, want tool_result", i, content.Type)
		}
	}
	if toolResults.Content[0].ToolUseId != "call_1" || toolResults.Content[1].ToolUseId != "call_2" || toolResults.Content[2].ToolUseId != "call_3" {
		t.Fatalf("tool result ids = %#v", toolResults.Content)
	}
}

func TestConvertInboundRequestRoundTripLiftsSystemMessages(t *testing.T) {
	request := InboundRequest{
		Model:     "ark-code-latest-claude",
		MaxTokens: 128,
		System: []any{
			map[string]any{"type": "text", "text": "top-level system"},
		},
		Messages: []InboundMessage{
			{
				Role:    "user",
				Content: "Install the tool.",
			},
			{
				Role:    "system",
				Content: "available tools and skills",
			},
		},
	}

	openAIRequest, err := ConvertInboundRequest(request)
	if err != nil {
		t.Fatalf("ConvertInboundRequest returned error: %v", err)
	}

	roundTripped := ConvertRequest(*openAIRequest)
	if roundTripped.System != "top-level system\navailable tools and skills" {
		t.Fatalf("system = %q", roundTripped.System)
	}
	if len(roundTripped.Messages) != 1 {
		t.Fatalf("messages length = %d, want 1: %#v", len(roundTripped.Messages), roundTripped.Messages)
	}
	if roundTripped.Messages[0].Role == "system" {
		t.Fatalf("system message leaked into messages: %#v", roundTripped.Messages)
	}
}

func TestConvertRequestPrependsToolResultsBeforeInterruptedUserText(t *testing.T) {
	openAIRequest, err := ConvertInboundRequest(InboundRequest{
		Model:     "ark-code-latest-claude",
		MaxTokens: 128,
		Messages: []InboundMessage{
			{
				Role:    "user",
				Content: "Create tasks.",
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "call_1",
						"name":  "TaskCreate",
						"input": map[string]any{"subject": "one"},
					},
					map[string]any{
						"type":  "tool_use",
						"id":    "call_2",
						"name":  "TaskCreate",
						"input": map[string]any{"subject": "two"},
					},
				},
			},
			{
				Role:    "user",
				Content: "Create tasks.",
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "call_1",
						"content":     "Task #1 created",
					},
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "call_2",
						"content":     "Task #2 created",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ConvertInboundRequest returned error: %v", err)
	}

	roundTripped := ConvertRequest(*openAIRequest)
	if len(roundTripped.Messages) != 3 {
		t.Fatalf("messages length = %d, want 3: %#v", len(roundTripped.Messages), roundTripped.Messages)
	}
	userMessage := roundTripped.Messages[2]
	if userMessage.Role != "user" {
		t.Fatalf("user message role = %q", userMessage.Role)
	}
	if len(userMessage.Content) != 3 {
		t.Fatalf("user content length = %d, want 3: %#v", len(userMessage.Content), userMessage.Content)
	}
	if userMessage.Content[0].Type != "tool_result" || userMessage.Content[1].Type != "tool_result" || userMessage.Content[2].Type != "text" {
		t.Fatalf("user content order = %#v", userMessage.Content)
	}
}
