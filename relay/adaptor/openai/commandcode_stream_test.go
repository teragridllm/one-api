package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

type commandCodeDoneRecorder struct {
	*httptest.ResponseRecorder
	onDone func()
}

func (w *commandCodeDoneRecorder) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if err == nil && w.onDone != nil && strings.Contains(string(p[:n]), "[DONE]") {
		onDone := w.onDone
		w.onDone = nil
		onDone()
	}
	return n, err
}

func (w *commandCodeDoneRecorder) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func TestCommandCodeStreamReusesConnectionAfterDone(t *testing.T) {
	stream := commandCodeFinishEvent + commandCodeUsageEvent + "data: [DONE]\n\n"
	doneSignals := make(chan chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamDone := <-doneSignals
		if _, err := io.WriteString(w, stream); err != nil {
			t.Errorf("write stream: %v", err)
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-downstreamDone:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	connections := make(chan bool, 3)
	for i := 0; i < 3; i++ {
		ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { connections <- info.Reused },
		})
		downstreamDone := make(chan struct{})
		doneSignals <- downstreamDone
		w := &commandCodeDoneRecorder{ResponseRecorder: httptest.NewRecorder(), onDone: func() { close(downstreamDone) }}
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		a := &Adaptor{}
		resp, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: srv.URL, RequestURLPath: "/v1/chat/completions"}, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		streamErr, _, usage := commandCodeStreamHandler(c, resp, relaymode.ChatCompletions)
		if streamErr != nil || usage == nil || !strings.Contains(w.Body.String(), "[DONE]") {
			t.Fatalf("stream error=%v usage=%+v body=%s", streamErr, usage, w.Body.String())
		}
		if reused := <-connections; reused != (i > 0) {
			t.Fatalf("request %d reused=%v, want %v", i+1, reused, i > 0)
		}
	}
}

func TestCommandCodeStreamDrainIsBounded(t *testing.T) {
	for _, scenario := range []string{"stalled EOF", "caller cancellation", "excess trailing bytes", "invalid DONE"} {
		t.Run(scenario, func(t *testing.T) {
			serverDone := make(chan struct{})
			stream := commandCodeFinishEvent + commandCodeUsageEvent + "data: [DONE]\n\n"
			if scenario == "invalid DONE" {
				stream = commandCodeFinishEvent + "data: [DONE]\n\n"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(serverDone)
				if _, err := io.WriteString(w, stream); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				if scenario == "excess trailing bytes" {
					if _, err := io.WriteString(w, strings.Repeat(" ", 128*1024)); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &commandCodeDoneRecorder{ResponseRecorder: httptest.NewRecorder()}
			if scenario == "caller cancellation" {
				w.onDone = cancel
			}
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			a := &Adaptor{}
			resp, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: srv.URL, RequestURLPath: "/v1/chat/completions"}, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			streamErr, _, usage := commandCodeStreamHandler(c, resp, relaymode.ChatCompletions)
			elapsed := time.Since(start)
			limit := 500 * time.Millisecond
			if scenario == "stalled EOF" {
				limit = 1500 * time.Millisecond
				if cause := context.Cause(resp.Body.(*commandCodeBody).ctx); !errors.Is(cause, errCommandCodeDrainTimeout) {
					t.Fatalf("drain cancellation cause=%v", cause)
				}
			}
			if elapsed > limit {
				t.Fatalf("drain elapsed=%s, limit=%s", elapsed, limit)
			}
			if scenario == "invalid DONE" {
				if streamErr == nil || usage != nil || strings.Contains(w.Body.String(), "[DONE]") {
					t.Fatalf("invalid DONE accepted: error=%v usage=%+v", streamErr, usage)
				}
			} else if streamErr != nil || usage == nil || !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("drain changed completed result: error=%v usage=%+v body=%s", streamErr, usage, w.Body.String())
			}
			select {
			case <-serverDone:
			case <-time.After(time.Second):
				t.Fatal("upstream request still running after handler returned")
			}
		})
	}
}

const commandCodeRoleEvent = "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"
const commandCodeFinishEvent = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
const commandCodeUsageEvent = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\n"

type commandCodeBrokenReader struct{}

func (commandCodeBrokenReader) Read([]byte) (int, error) {
	return 0, errors.New("test stream reset")
}

func TestCommandCodeStreamReportsFailuresWithoutDone(t *testing.T) {
	tests := []struct {
		name    string
		body    io.Reader
		started bool
	}{
		{"read reset", io.MultiReader(strings.NewReader(commandCodeRoleEvent), commandCodeBrokenReader{}), true},
		{"early EOF", strings.NewReader(commandCodeRoleEvent), true},
		{"missing finish", strings.NewReader(commandCodeRoleEvent + commandCodeUsageEvent + "data: [DONE]\n\n"), true},
		{"missing usage", strings.NewReader(commandCodeRoleEvent + commandCodeFinishEvent + "data: [DONE]\n\n"), true},
		{"upstream error", strings.NewReader("data: {\"error\":{\"type\":\"server_error\",\"message\":\"overloaded\"}}\n\n"), false},
		{"midstream error", strings.NewReader(commandCodeRoleEvent + "data: {\"error\":{\"message\":\"overloaded\"}}\n\n"), true},
		{"malformed event", strings.NewReader("data: {broken\n\n"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			a := &Adaptor{}
			usage, err := a.DoResponse(c, &http.Response{StatusCode: 200, Body: io.NopCloser(tt.body)}, &meta.Meta{ChannelType: channeltype.CommandCode, IsStream: true, Mode: relaymode.ChatCompletions})
			if err == nil || err.StatusCode != http.StatusBadGateway {
				t.Fatalf("expected stream failure, got %v", err)
			}
			if usage != nil {
				t.Fatalf("failed stream must not produce billable usage: %+v", usage)
			}
			if strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("failed stream reported completion: %s", w.Body.String())
			}
			if c.Writer.Written() != tt.started {
				t.Fatalf("written=%v, want %v", c.Writer.Written(), tt.started)
			}
			if tt.started && !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("missing SSE error: %s", w.Body.String())
			}
			if !tt.started && w.Header().Get("Content-Type") != "" {
				t.Fatal("failed first event must leave JSON error headers available")
			}
		})
	}
}

func TestCommandCodeStreamPreservesReasoningToolsAndUsage(t *testing.T) {
	stream := commandCodeRoleEvent +
		"data:{\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n" +
		strings.ReplaceAll(commandCodeFinishEvent, "stop", "tool_calls") + commandCodeUsageEvent + "data: [DONE]\n\n"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	err, _, usage := commandCodeStreamHandler(c, &http.Response{Body: io.NopCloser(io.MultiReader(strings.NewReader(stream), commandCodeBrokenReader{}))}, relaymode.ChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.TotalTokens != 13 {
		t.Fatalf("usage=%+v", usage)
	}
	for _, field := range []string{`"reasoning_content"`, `"tool_calls"`, `"usage"`, "[DONE]"} {
		if !strings.Contains(w.Body.String(), field) {
			t.Fatalf("missing %s in stream", field)
		}
	}
}

func TestCommandCodeStreamRequiresEveryChoiceToFinish(t *testing.T) {
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{},\"finish_reason\":null}]}\n\n" + commandCodeUsageEvent + "data: [DONE]\n\n"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	err, _, _ := commandCodeStreamHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(stream))}, relaymode.ChatCompletions)
	if err == nil || strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatal("unfinished choice accepted")
	}
}

func TestCommandCodeStreamHandlesMultilineSSE(t *testing.T) {
	stream := ": keepalive\r\n\r\nevent: message\r\ndata: {\"choices\":\r\ndata: [{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\n" + commandCodeUsageEvent + "data: [DONE]"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	err, text, _ := commandCodeStreamHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(stream))}, relaymode.ChatCompletions)
	if err != nil || text != "ok" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	var data []string
	for _, line := range strings.Split(strings.SplitN(w.Body.String(), "\n\n", 2)[0], "\n") {
		if !strings.HasPrefix(line, "data:") {
			t.Fatalf("missing SSE data field: %q", line)
		}
		data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
	}
	if !json.Valid([]byte(strings.Join(data, "\n"))) {
		t.Fatalf("invalid multiline SSE JSON: %s", w.Body.String())
	}
}
