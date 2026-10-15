package controller

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
)

func TestCommandCodeRequestPreservesExtensionsWhenMappingModel(t *testing.T) {
	raw := `{"model":"alias","stream":true,"thinking":{"type":"disabled"},"stream_options":{"include_usage":false,"extra":true},"messages":[{"role":"assistant","content":null,"reasoning_content":"thought","extra":"keep","tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}],"tools":[{"type":"function","function":{"name":"lookup","strict":true,"parameters":{"type":"object"}}}]}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(raw))
	request := &model.GeneralOpenAIRequest{Model: "deepseek/deepseek-v4-flash", Stream: true}
	meta := &meta.Meta{ChannelType: channeltype.CommandCode, OriginModelName: "alias", ActualModelName: request.Model}
	body, err := getRequestBody(c, meta, request, &openai.Adaptor{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Model    string                       `json:"model"`
		Thinking map[string]string            `json:"thinking"`
		Options  map[string]bool              `json:"stream_options"`
		Messages []map[string]json.RawMessage `json:"messages"`
		Tools    []struct {
			Function struct {
				Strict bool `json:"strict"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Model != request.Model || result.Thinking["type"] != "disabled" || !result.Options["include_usage"] || !result.Options["extra"] || !result.Tools[0].Function.Strict {
		t.Fatalf("extensions lost: %s", data)
	}
	if string(result.Messages[0]["content"]) != "null" || string(result.Messages[0]["extra"]) != `"keep"` {
		t.Fatalf("message changed: %s", data)
	}
}

func TestCommandCodeRequestAppliesSystemPromptWithoutDroppingMessageFields(t *testing.T) {
	for _, role := range []string{"system", "user"} {
		t.Run(role, func(t *testing.T) {
			raw := `{"model":"m","messages":[{"role":"` + role + `","content":"old","extra":"keep"}]}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(raw))
			body, err := commandCodeRequestBody(c, &meta.Meta{ForcedSystemPrompt: "forced"}, &model.GeneralOpenAIRequest{Model: "m"})
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Messages []map[string]string `json:"messages"`
			}
			if err := json.NewDecoder(body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if result.Messages[0]["role"] != "system" || result.Messages[0]["content"] != "forced" {
				t.Fatalf("prompt not applied: %+v", result)
			}
			idx := 0
			if role == "user" {
				idx = 1
			}
			if result.Messages[idx]["extra"] != "keep" {
				t.Fatal("message extension dropped")
			}
		})
	}
}
