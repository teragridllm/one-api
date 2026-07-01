package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/songquanpeng/one-api/relay/model"
)

func appendSystemPrompt(system string, prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return system
	}
	if system == "" {
		return prompt
	}
	return system + "\n" + prompt
}

func appendToolCallContent(contents []Content, toolCalls []model.Tool) []Content {
	for i := range toolCalls {
		inputParam := make(map[string]any)
		if args, ok := toolCalls[i].Function.Arguments.(string); ok {
			_ = json.Unmarshal([]byte(args), &inputParam)
		}
		contents = append(contents, Content{
			Type:  "tool_use",
			Id:    toolCalls[i].Id,
			Name:  toolCalls[i].Function.Name,
			Input: inputParam,
		})
	}
	return contents
}

func appendClaudeMessage(messages []Message, message Message) []Message {
	if !isToolResultMessage(message) || len(messages) == 0 {
		return append(messages, message)
	}
	last := &messages[len(messages)-1]
	if !isToolResultMessage(*last) {
		if shouldPrependToolResult(messages) {
			last.Content = append(message.Content, last.Content...)
			return messages
		}
		return append(messages, message)
	}
	last.Content = append(last.Content, message.Content...)
	return messages
}

func shouldPrependToolResult(messages []Message) bool {
	if len(messages) < 2 {
		return false
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || isToolResultMessage(last) {
		return false
	}
	previous := messages[len(messages)-2]
	if previous.Role != "assistant" {
		return false
	}
	for _, content := range previous.Content {
		if content.Type == "tool_use" {
			return true
		}
	}
	return false
}

func isToolResultMessage(message Message) bool {
	if message.Role != "user" || len(message.Content) == 0 {
		return false
	}
	for _, content := range message.Content {
		if content.Type != "tool_result" {
			return false
		}
	}
	return true
}
