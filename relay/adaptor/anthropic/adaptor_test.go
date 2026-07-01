package anthropic

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
)

func TestAdaptorGetRequestURLWhenAnthropicCompatible(t *testing.T) {
	adaptor := &Adaptor{}

	requestURL, err := adaptor.GetRequestURL(&meta.Meta{
		ChannelType: channeltype.AnthropicCompatible,
		BaseURL:     "https://ark.cn-beijing.volces.com/api/coding",
	})

	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if requestURL != "https://ark.cn-beijing.volces.com/api/coding/v1/messages" {
		t.Fatalf("request URL = %q, want %q", requestURL, "https://ark.cn-beijing.volces.com/api/coding/v1/messages")
	}
}

func TestAdaptorGetRequestURLWhenAnthropic(t *testing.T) {
	adaptor := &Adaptor{}

	requestURL, err := adaptor.GetRequestURL(&meta.Meta{
		ChannelType: channeltype.Anthropic,
		BaseURL:     "https://example.com",
	})

	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if requestURL != "https://example.com/v1/messages" {
		t.Fatalf("request URL = %q, want %q", requestURL, "https://example.com/v1/messages")
	}
}

func TestAdaptorSetupRequestHeaderWhenAnthropicCompatible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{Header: make(http.Header)}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}

	adaptor := &Adaptor{}
	err = adaptor.SetupRequestHeader(c, req, &meta.Meta{
		ChannelType: channeltype.AnthropicCompatible,
		APIKey:      "test-key",
	})

	if err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if req.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want %q", req.Header.Get("Authorization"), "Bearer test-key")
	}
	if req.Header.Get("x-api-key") != "" {
		t.Fatalf("x-api-key = %q, want empty", req.Header.Get("x-api-key"))
	}
}

func TestAdaptorSetupRequestHeaderWhenAnthropic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{Header: make(http.Header)}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}

	adaptor := &Adaptor{}
	err = adaptor.SetupRequestHeader(c, req, &meta.Meta{
		ChannelType: channeltype.Anthropic,
		APIKey:      "test-key",
	})

	if err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if req.Header.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want %q", req.Header.Get("x-api-key"), "test-key")
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization = %q, want empty", req.Header.Get("Authorization"))
	}
}
