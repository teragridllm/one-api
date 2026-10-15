package openai

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

func TestAdaptorSetupRequestHeaderForwardsSessionForOpenCode(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{Header: make(http.Header)}
	c.Request.Header.Set("x-opencode-session", "session-123")
	outboundRequest, err := http.NewRequest(http.MethodPost, "https://opencode.ai/inference/go/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}

	// When
	adaptor := &Adaptor{}
	err = adaptor.SetupRequestHeader(c, outboundRequest, &meta.Meta{
		ChannelType: channeltype.OpenCode,
		APIKey:      "test-key",
	})

	// Then
	if err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := outboundRequest.Header.Get("x-opencode-session"); got != "session-123" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "session-123")
	}
}

func TestAdaptorSetupRequestHeaderGeneratesSessionForOpenCodeWhenMissing(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{Header: make(http.Header)}
	outboundRequest, err := http.NewRequest(http.MethodPost, "https://opencode.ai/inference/go/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}

	// When
	adaptor := &Adaptor{}
	err = adaptor.SetupRequestHeader(c, outboundRequest, &meta.Meta{
		ChannelType: channeltype.OpenCode,
		APIKey:      "test-key",
	})

	// Then
	if err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := outboundRequest.Header.Get("x-opencode-session"); got == "" {
		t.Fatal("x-opencode-session is empty for OpenCode request without an inbound session")
	}
}

func TestAdaptorSetupRequestHeaderDoesNotForwardSessionForOpenAICompatible(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{Header: make(http.Header)}
	c.Request.Header.Set("x-opencode-session", "session-123")
	outboundRequest, err := http.NewRequest(http.MethodPost, "https://example.com/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}

	// When
	adaptor := &Adaptor{}
	err = adaptor.SetupRequestHeader(c, outboundRequest, &meta.Meta{
		ChannelType: channeltype.OpenAICompatible,
		APIKey:      "test-key",
	})

	// Then
	if err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := outboundRequest.Header.Get("x-opencode-session"); got != "" {
		t.Fatalf("x-opencode-session = %q, want empty for OpenAI-compatible channel", got)
	}
}

func TestAdaptorGetRequestURLUsesCompatiblePathForOpenCode(t *testing.T) {
	// Given
	adaptor := &Adaptor{}

	// When
	requestURL, err := adaptor.GetRequestURL(&meta.Meta{
		ChannelType:    channeltype.OpenCode,
		BaseURL:        "https://opencode.ai/inference/go/v1",
		RequestURLPath: "/v1/chat/completions",
	})

	// Then
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if requestURL != "https://opencode.ai/inference/go/v1/chat/completions" {
		t.Fatalf("request URL = %q, want %q", requestURL, "https://opencode.ai/inference/go/v1/chat/completions")
	}
}

func TestAdaptorConvertRequestNormalizesAssistantMessagesOnlyForOpenCode(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		wantPresent bool
	}{
		{name: "OpenCode", channelType: channeltype.OpenCode, wantPresent: true},
		{name: "OpenAI compatible", channelType: channeltype.OpenAICompatible, wantPresent: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adaptor := &Adaptor{}
			adaptor.Init(&meta.Meta{ChannelType: test.channelType})
			request := &model.GeneralOpenAIRequest{
				Messages: []model.Message{
					{Role: "assistant", Content: "done", ReasoningContent: "thought"},
					{Role: "assistant", Content: "", ToolCalls: []model.Tool{{Type: "function"}}},
					{Role: "assistant", ReasoningContent: "thought only"},
				},
				Tools: []model.Tool{{Type: "function"}},
			}

			converted, err := adaptor.ConvertRequest(nil, relaymode.ChatCompletions, request)
			if err != nil {
				t.Fatalf("ConvertRequest returned error: %v", err)
			}
			encoded, err := json.Marshal(converted)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			var payload struct {
				Messages []map[string]any `json:"messages"`
			}
			if err = json.Unmarshal(encoded, &payload); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			_, present := payload.Messages[1]["reasoning_content"]
			if present != test.wantPresent {
				t.Fatalf("reasoning_content presence = %v, want %v; payload: %s", present, test.wantPresent, encoded)
			}
			_, present = payload.Messages[2]["content"]
			if present != test.wantPresent {
				t.Fatalf("content presence = %v, want %v; payload: %s", present, test.wantPresent, encoded)
			}
		})
	}
}
