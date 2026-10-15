package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/apitype"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

func TestGetRequestBodyAddsMissingReasoningContentWhenChannelIsOpenCode(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	rawBody := []byte(`{"model":"deepseek-v4.1-flash","messages":[{"role":"assistant","content":"done","reasoning_content":"thought"},{"role":"assistant","content":"","tool_calls":[{"type":"function","function":{"name":"get_weather","arguments":"{}"}}]}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`)
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(rawBody))
	relayMeta := &meta.Meta{
		APIType:         apitype.OpenAI,
		Mode:            relaymode.ChatCompletions,
		ChannelType:     channeltype.OpenCode,
		OriginModelName: "deepseek-v4.1-flash",
		ActualModelName: "deepseek-v4.1-flash",
	}
	request := &model.GeneralOpenAIRequest{
		Model: "deepseek-v4.1-flash",
		Messages: []model.Message{
			{Role: "assistant", Content: "done", ReasoningContent: "thought"},
			{Role: "assistant", Content: "", ToolCalls: []model.Tool{{Type: "function"}}},
		},
		Tools: []model.Tool{{Type: "function"}},
	}
	openAIAdaptor := &openai.Adaptor{}
	openAIAdaptor.Init(relayMeta)

	// When
	body, err := getRequestBody(c, relayMeta, request, openAIAdaptor)
	if err != nil {
		t.Fatalf("getRequestBody returned error: %v", err)
	}
	encoded, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("io.ReadAll returned error: %v", err)
	}

	// Then
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err = json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if _, present := payload.Messages[1]["reasoning_content"]; !present {
		t.Fatalf("OpenCode request bypassed conversion and omitted reasoning_content: %s", encoded)
	}
}
