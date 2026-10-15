package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/relay/apitype"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
)

func TestCommandCodeChannelDefaults(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(ctxkey.Channel, channeltype.CommandCode)
	m := meta.GetByContext(c)
	a := &Adaptor{}
	got, err := a.GetRequestURL(m)
	if err != nil || got != "https://api.commandcode.ai/provider/v1/chat/completions" {
		t.Fatalf("default URL=%s, err=%v", got, err)
	}
	if m.APIType != apitype.OpenAI || channeltype.CommandCode != 54 {
		t.Fatalf("unexpected channel mapping: %+v", m)
	}
	m.BaseURL += "/"
	withSlash, err := a.GetRequestURL(m)
	if err != nil || withSlash != got {
		t.Fatalf("trailing slash URL=%s, err=%v", withSlash, err)
	}
}

func TestCommandCodeClientNegotiatesHTTP1WithHTTP2Server(t *testing.T) {
	protocol := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol <- r.Proto
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	client, err := newCommandCodeClient("", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tr := client.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer tr.CloseIdleConnections()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-protocol; got != "HTTP/1.1" {
		t.Fatalf("protocol=%s", got)
	}
}

func TestCommandCodeRequestReplaysBodyAndForwardsProviderHeader(t *testing.T) {
	const payload = `{"model":"m","messages":[]}`
	received := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		received <- r.Method + "|" + string(data) + "|" + r.Header.Get("X-Cmd-Zdr") + "|" + r.Header.Get("X-Opencode-Session") + "|" + r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("x-cmd-zdr", "1")
	c.Request.Header.Set("x-opencode-session", "must-not-forward")
	a := &Adaptor{}
	resp, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: srv.URL, RequestURLPath: "/v1/chat/completions", APIKey: "test-key"}, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != "POST|"+payload+"|1||Bearer test-key" {
		t.Fatalf("request=%s", got)
	}
}

func TestCommandCodeRequestHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	a := &Adaptor{}
	_, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: "http://127.0.0.1:1", RequestURLPath: "/v1/chat/completions"}, strings.NewReader("{}"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled request, got %v", err)
	}
}

func TestCommandCodeRequestSharesConfiguredTimeoutAcrossRetryAndBody(t *testing.T) {
	t.Setenv("COMMANDCODE_RESPONSE_IDLE_TIMEOUT", "0")
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}), func(n int64) bool { return n == 1 })
	client.Timeout = 700 * time.Millisecond
	originalClient := commandCodeClient
	commandCodeClient = func() (*http.Client, error) { return client, nil }
	t.Cleanup(func() { commandCodeClient = originalClient })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	start := time.Now()
	a := &Adaptor{}
	resp, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: srv.URL, RequestURLPath: "/v1/chat/completions"}, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := resp.Body.(*commandCodeBody)
	deadline, ok := body.ctx.Deadline()
	if !ok || deadline.Before(start) || deadline.After(start.Add(client.Timeout+50*time.Millisecond)) {
		t.Fatalf("configured deadline missing or reset by retry: start=%s deadline=%s", start, deadline)
	}
	_, err = io.ReadAll(body)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(body.ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("body did not retain total deadline: read=%v context=%v", err, body.ctx.Err())
	}
	if got := handshakes.Load(); got != 2 {
		t.Fatalf("handshakes=%d, want 2", got)
	}
}

func TestCommandCodeRequestUsesConfiguredIdleTimeout(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"", 300 * time.Second}, {"120", 120 * time.Second}, {"0", 0}, {"-1", 300 * time.Second}} {
		t.Run("idle="+tc.value, func(t *testing.T) {
			t.Setenv("COMMANDCODE_RESPONSE_IDLE_TIMEOUT", tc.value)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			a := &Adaptor{}
			resp, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: srv.URL, RequestURLPath: "/v1/chat/completions"}, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			body := resp.Body.(*commandCodeBody)
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			if body.idle != tc.want {
				t.Fatalf("idle timeout=%s, want %s", body.idle, tc.want)
			}
		})
	}
}

func TestCommandCodeBodyCancelsStalledStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := srv.Client()
	client.Timeout = time.Second
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := &commandCodeBody{ReadCloser: resp.Body, ctx: ctx, cancel: cancel, idle: 10 * time.Millisecond}
	_, err = io.ReadAll(body)
	if !errors.Is(err, errCommandCodeIdle) {
		t.Fatalf("expected idle timeout, got %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandCodeBodyDrainCapsBytes(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	body := &commandCodeBody{
		ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat(" ", 128*1024))),
		ctx:        ctx, cancel: cancel,
	}
	if err := body.drainForReuse(); !errors.Is(err, errCommandCodeDrainLimit) {
		t.Fatalf("drain error=%v", err)
	}
	if body.received != 64*1024 {
		t.Fatalf("drained bytes=%d, want 65536", body.received)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandCodeDisabledIdleTimeoutStillHonorsRequestDeadline(t *testing.T) {
	t.Setenv("COMMANDCODE_RESPONSE_IDLE_TIMEOUT", "0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	a := &Adaptor{}
	resp, err := a.DoRequest(c, &meta.Meta{ChannelType: channeltype.CommandCode, BaseURL: srv.URL, RequestURLPath: "/v1/chat/completions"}, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		t.Fatalf("disabled idle timeout aborted before caller cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-readDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected caller cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not stop response read")
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
}
