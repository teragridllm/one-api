package openai

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/env"
	"github.com/songquanpeng/one-api/relay/meta"
)

func commandCodeStreamIdleTimeout() time.Duration {
	seconds := env.Int("COMMANDCODE_RESPONSE_IDLE_TIMEOUT", 300)
	if seconds < 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

var commandCodeClient = sync.OnceValues(func() (*http.Client, error) {
	return newCommandCodeClient(config.RelayProxy, time.Duration(config.RelayTimeout)*time.Second)
})

func newCommandCodeClient(proxy string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	transport.ResponseHeaderTimeout = 45 * time.Second
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("parse CommandCode relay proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func (a *Adaptor) doCommandCodeRequest(c *gin.Context, relayMeta *meta.Meta, body io.Reader) (*http.Response, error) {
	client, err := commandCodeClient()
	if err != nil {
		return nil, err
	}
	requestURL, err := a.GetRequestURL(relayMeta)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read CommandCode request body: %w", err)
	}
	ctx, cancel := context.WithCancelCause(c.Request.Context())
	if client.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, client.Timeout)
		cancelCause := cancel
		cancel = func(cause error) {
			cancelCause(cause)
			timeoutCancel()
		}
	}
	req, err := http.NewRequestWithContext(ctx, c.Request.Method, requestURL, bytes.NewReader(data))
	if err != nil {
		cancel(err)
		return nil, err
	}
	if err := a.SetupRequestHeader(c, req, relayMeta); err != nil {
		cancel(err)
		return nil, err
	}
	resp, err := doCommandCodeWithTLSRetry(client, req)
	if err != nil {
		cancel(err)
		return nil, fmt.Errorf("CommandCode request failed: %w", err)
	}
	resp.Body = &commandCodeBody{ReadCloser: resp.Body, ctx: ctx, cancel: cancel, idle: commandCodeStreamIdleTimeout()}
	return resp, nil
}

type commandCodeBody struct {
	io.ReadCloser
	ctx      context.Context
	cancel   context.CancelCauseFunc
	idle     time.Duration
	received int64
}

var errCommandCodeIdle = errors.New("CommandCode response idle timeout")
var errCommandCodeDrainTimeout = errors.New("CommandCode completed response drain timeout")
var errCommandCodeDrainLimit = errors.New("CommandCode completed response drain exceeds 64 KiB")

func (b *commandCodeBody) drainForReuse() error {
	timer := time.AfterFunc(time.Second, func() { b.cancel(errCommandCodeDrainTimeout) })
	defer timer.Stop()
	_, err := io.CopyN(io.Discard, b, 64*1024)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errCommandCodeDrainLimit
	}
	return err
}

func (b *commandCodeBody) Read(p []byte) (int, error) {
	var timer *time.Timer
	if b.idle > 0 {
		timer = time.AfterFunc(b.idle, func() { b.cancel(errCommandCodeIdle) })
	}
	n, err := b.ReadCloser.Read(p)
	if timer != nil {
		timer.Stop()
	}
	b.received += int64(n)
	if cause := context.Cause(b.ctx); cause != nil {
		if errors.Is(cause, errCommandCodeIdle) {
			return n, fmt.Errorf("%w (no upstream data for %s; received %d body bytes)", cause, b.idle, b.received)
		}
		return n, cause
	}
	return n, err
}

func (b *commandCodeBody) Close() error {
	b.cancel(nil)
	return b.ReadCloser.Close()
}
