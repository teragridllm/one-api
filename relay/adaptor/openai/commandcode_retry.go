package openai

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
)

// A Do can follow redirects. Once any connection is acquired, replaying the
// original POST is unsafe even if a later redirect fails its TLS handshake.
type commandCodeAttemptTrace struct {
	mu         sync.Mutex
	acquired   bool
	tlsTimeout error
}

func (s *commandCodeAttemptTrace) markAcquired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquired = true
}

func (s *commandCodeAttemptTrace) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				s.mu.Lock()
				defer s.mu.Unlock()
				s.tlsTimeout = err
			}
		},
		GotConn:      func(httptrace.GotConnInfo) { s.markAcquired() },
		WroteHeaders: s.markAcquired,
		WroteRequest: func(httptrace.WroteRequestInfo) { s.markAcquired() },
	}
}

func (s *commandCodeAttemptTrace) canRetry(err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.acquired && s.tlsTimeout != nil && errors.Is(err, s.tlsTimeout)
}

func doCommandCodeWithTLSRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	state := &commandCodeAttemptTrace{}
	first := req.Clone(httptrace.WithClientTrace(req.Context(), state.trace()))
	resp, err := client.Do(first)
	if err == nil || req.Context().Err() != nil || !state.canRetry(err) || (req.Body != nil && req.Body != http.NoBody && req.GetBody == nil) {
		return resp, err
	}

	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-timer.C:
	}
	if req.Context().Err() != nil {
		return nil, req.Context().Err()
	}
	secondState := &commandCodeAttemptTrace{}
	second := req.Clone(httptrace.WithClientTrace(req.Context(), secondState.trace()))
	if req.GetBody != nil {
		body, replayErr := req.GetBody()
		if replayErr != nil {
			return resp, err
		}
		second.Body = body
	}
	logger.Warnf(req.Context(), "CommandCode TLS handshake timeout before HTTP request; retry attempt=2")
	return client.Do(second)
}
