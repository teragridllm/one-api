package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

type InboundRequest struct {
	Model         string           `json:"model"`
	Messages      []InboundMessage `json:"messages"`
	System        any              `json:"system,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	StopSequences any              `json:"stop_sequences,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	TopK          int              `json:"top_k,omitempty"`
	Tools         []InboundTool    `json:"tools,omitempty"`
	ToolChoice    any              `json:"tool_choice,omitempty"`
}

type InboundMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type InboundTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

func ConvertInboundRequest(request InboundRequest) (*relaymodel.GeneralOpenAIRequest, error) {
	openaiRequest := &relaymodel.GeneralOpenAIRequest{
		Model:       request.Model,
		MaxTokens:   request.MaxTokens,
		Stream:      request.Stream,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		TopK:        request.TopK,
		Stop:        request.StopSequences,
		Tools:       convertInboundTools(request.Tools),
		ToolChoice:  convertInboundToolChoice(request.ToolChoice),
	}

	if system := systemContent(request.System); system != "" {
		openaiRequest.Messages = append(openaiRequest.Messages, relaymodel.Message{
			Role:    "system",
			Content: system,
		})
	}

	for _, message := range request.Messages {
		convertedMessages, err := convertInboundMessage(message)
		if err != nil {
			return nil, err
		}
		openaiRequest.Messages = append(openaiRequest.Messages, convertedMessages...)
	}

	return openaiRequest, nil
}

func convertInboundTools(tools []InboundTool) []relaymodel.Tool {
	openaiTools := make([]relaymodel.Tool, 0, len(tools))
	for _, tool := range tools {
		openaiTools = append(openaiTools, relaymodel.Tool{
			Type: "function",
			Function: relaymodel.Function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return openaiTools
}

func convertInboundToolChoice(toolChoice any) any {
	choiceMap, ok := toolChoice.(map[string]any)
	if !ok {
		return toolChoice
	}
	choiceType, _ := choiceMap["type"].(string)
	switch choiceType {
	case "tool":
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": choiceMap["name"],
			},
		}
	case "auto", "any":
		return choiceType
	default:
		return toolChoice
	}
}

func convertInboundMessage(message InboundMessage) ([]relaymodel.Message, error) {
	switch content := message.Content.(type) {
	case string:
		return []relaymodel.Message{{
			Role:    message.Role,
			Content: content,
		}}, nil
	case []any:
		return convertInboundContentBlocks(message.Role, content)
	default:
		return nil, fmt.Errorf("unsupported anthropic content type %T", message.Content)
	}
}

func convertInboundContentBlocks(role string, blocks []any) ([]relaymodel.Message, error) {
	openaiContent := make([]any, 0, len(blocks))
	toolCalls := make([]relaymodel.Tool, 0)
	messages := make([]relaymodel.Message, 0, 1)

	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := blockMap["type"].(string)
		switch blockType {
		case "text":
			openaiContent = append(openaiContent, map[string]any{
				"type": "text",
				"text": blockMap["text"],
			})
		case "image":
			if imageContent := convertInboundImage(blockMap); imageContent != nil {
				openaiContent = append(openaiContent, imageContent)
			}
		case "tool_use":
			toolCall, err := convertInboundToolUse(blockMap)
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, toolCall)
		case "tool_result":
			messages = append(messages, relaymodel.Message{
				Role:       "tool",
				ToolCallId: stringFromMap(blockMap, "tool_use_id"),
				Content:    toolResultContent(blockMap["content"]),
			})
		}
	}

	if len(openaiContent) > 0 || len(toolCalls) > 0 {
		messages = append([]relaymodel.Message{{
			Role:      role,
			Content:   simplifyOpenAIContent(openaiContent),
			ToolCalls: toolCalls,
		}}, messages...)
	}
	return messages, nil
}

func convertInboundImage(block map[string]any) map[string]any {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil
	}
	mediaType := stringFromMap(source, "media_type")
	data := stringFromMap(source, "data")
	if mediaType == "" || data == "" {
		return nil
	}
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": fmt.Sprintf("data:%s;base64,%s", mediaType, data),
		},
	}
}

func convertInboundToolUse(block map[string]any) (relaymodel.Tool, error) {
	inputJSON, err := json.Marshal(block["input"])
	if err != nil {
		return relaymodel.Tool{}, fmt.Errorf("marshal tool input failed: %w", err)
	}
	return relaymodel.Tool{
		Id:   stringFromMap(block, "id"),
		Type: "function",
		Function: relaymodel.Function{
			Name:      stringFromMap(block, "name"),
			Arguments: string(inputJSON),
		},
	}, nil
}

func simplifyOpenAIContent(content []any) any {
	if len(content) == 1 {
		contentMap, ok := content[0].(map[string]any)
		if ok && contentMap["type"] == "text" {
			text, _ := contentMap["text"].(string)
			return text
		}
	}
	return content
}

func systemContent(system any) string {
	switch value := system.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, block := range value {
			blockMap, ok := block.(map[string]any)
			if !ok || blockMap["type"] != "text" {
				continue
			}
			if text, ok := blockMap["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func toolResultContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, block := range value {
			blockMap, ok := block.(map[string]any)
			if !ok || blockMap["type"] != "text" {
				continue
			}
			if text, ok := blockMap["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func stringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}
