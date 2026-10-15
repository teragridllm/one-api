package openai

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func commandCodeRetryTLSServer(t *testing.T, handler http.Handler, stall func(int64) bool) (*httptest.Server, *http.Client, *atomic.Int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	count := &atomic.Int64{}
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		if stall(count.Add(1)) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
	}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	t.Cleanup(cancel)
	client, err := newCommandCodeClient("", 0)
	require.NoError(t, err)
	tr := client.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	tr.TLSHandshakeTimeout = 100 * time.Millisecond
	t.Cleanup(tr.CloseIdleConnections)
	return srv, client, count
}

func commandCodeRetryRequest(t *testing.T, ctx context.Context, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	return req
}

func TestCommandCodeTLSRetryPreservesConcurrentTasks(t *testing.T) {
	// Given: task one is streaming when task two needs another TLS connection.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	var active, maximum, traceCalls atomic.Int64
	delivered := make(chan string, 2)
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read application payload: %v", err)
			return
		}
		delivered <- string(payload)
		n := active.Add(1)
		defer active.Add(-1)
		if n > maximum.Load() {
			maximum.Store(n)
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), func(n int64) bool { return n == 2 })
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{TLSHandshakeDone: func(tls.ConnectionState, error) { traceCalls.Add(1) }})
	first, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "task-one"))
	require.NoError(t, err)
	defer first.Body.Close()

	// When: task two's first handshake times out before its POST is transmitted.
	second, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "task-two-payload"))
	require.NoError(t, err)
	defer second.Body.Close()

	// Then: both tasks overlap, and each complete payload is delivered once.
	require.Equal(t, int64(2), maximum.Load())
	require.Equal(t, int64(3), handshakes.Load())
	require.Equal(t, int64(3), traceCalls.Load())
	require.Equal(t, "task-one", <-delivered)
	require.Equal(t, "task-two-payload", <-delivered)
	require.Empty(t, delivered)
	t.Logf("overlapping HTTP requests=%d TLS handshakes=%d application payloads=task-one,task-two-payload", maximum.Load(), handshakes.Load())
}

func TestCommandCodeTLSRetryStopsAfterSecondHandshakeTimeout(t *testing.T) {
	// Given
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("POST must not arrive") }), func(int64) bool { return true })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// When
	_, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "payload"))
	// Then
	require.ErrorContains(t, err, "TLS handshake timeout")
	require.Equal(t, int64(2), handshakes.Load())
}

func TestCommandCodeTLSRetryRequiresReplayableBody(t *testing.T) {
	// Given
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("POST must not arrive") }), func(int64) bool { return true })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := commandCodeRetryRequest(t, ctx, srv.URL, "payload")
	req.GetBody = nil
	// When
	_, err := doCommandCodeWithTLSRetry(client, req)
	// Then
	require.ErrorContains(t, err, "TLS handshake timeout")
	require.Equal(t, int64(1), handshakes.Load())
}

func TestCommandCodeTLSRetryHonorsCancellation(t *testing.T) {
	// Given
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("POST must not arrive") }), func(int64) bool { return true })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
		if err != nil {
			cancel()
		}
	}})
	// When
	_, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "payload"))
	// Then
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, int64(1), handshakes.Load())
}

func TestCommandCodeTLSRetryDoesNotReplayAfterRedirect(t *testing.T) {
	// Given: the original POST succeeds, then the redirected TLS handshake stalls.
	target, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect target must not receive POST") }), func(int64) bool { return true })
	var posts atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// When
	_, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, origin.URL, "payload"))
	// Then
	require.ErrorContains(t, err, "TLS handshake timeout")
	require.Equal(t, int64(1), posts.Load())
	require.Equal(t, int64(1), handshakes.Load())
}

func TestCommandCodeTLSRetryDoesNotReplayHeaderTimeout(t *testing.T) {
	// Given
	var posts atomic.Int64
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read application payload: %v", err)
			return
		}
		<-r.Context().Done()
	}), func(int64) bool { return false })
	client.Transport.(*http.Transport).ResponseHeaderTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// When
	_, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "payload"))
	// Then
	require.ErrorContains(t, err, "timeout awaiting response headers")
	require.Equal(t, int64(1), posts.Load())
	require.Equal(t, int64(1), handshakes.Load())
}

func TestCommandCodeTLSRetrySharesDeadlineAcrossAttemptsAndBody(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read application payload: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}), func(n int64) bool { return n == 1 })
	resp, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "payload"))
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int64(2), handshakes.Load())
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}

func TestCommandCodeTLSRetryDoesNotRetryCertificateFailure(t *testing.T) {
	// Given
	srv, client, handshakes := commandCodeRetryTLSServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("POST must not arrive") }), func(int64) bool { return false })
	client.Transport.(*http.Transport).TLSClientConfig.RootCAs = nil
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// When
	_, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "payload"))
	// Then
	var certificateError *tls.CertificateVerificationError
	require.ErrorAs(t, err, &certificateError)
	require.Equal(t, int64(1), handshakes.Load())
}

func TestCommandCodeTLSRetryDoesNotRetryHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// Given
			var posts atomic.Int64
			srv, client, _ := commandCodeRetryTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); w.WriteHeader(status) }), func(int64) bool { return false })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// When
			resp, err := doCommandCodeWithTLSRetry(client, commandCodeRetryRequest(t, ctx, srv.URL, "payload"))
			// Then
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, status, resp.StatusCode)
			require.Equal(t, int64(1), posts.Load())
		})
	}
}

func TestCommandCodeTLSRetryRejectsUnrelatedError(t *testing.T) {
	// Given
	state := &commandCodeAttemptTrace{}
	state.trace().TLSHandshakeDone(tls.ConnectionState{}, context.DeadlineExceeded)
	// When
	canRetry := state.canRetry(errors.New("unrelated request failure"))
	// Then
	require.False(t, canRetry)
}
