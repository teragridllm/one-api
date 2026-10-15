package openai

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/render"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

func commandCodeStreamHandler(c *gin.Context, resp *http.Response, mode int) (*model.ErrorWithStatusCode, string, *model.Usage) {
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Errorf(c.Request.Context(), "close CommandCode response: %s", err)
		}
	}()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	var text strings.Builder
	var usage *model.Usage
	choices := make(map[int]bool)
	var event []string
	eventBytes := 0
	fail := func(err error, code string) (*model.ErrorWithStatusCode, string, *model.Usage) {
		wrapped := ErrorWrapper(err, code, http.StatusBadGateway)
		if c.Writer.Written() {
			if writeErr := render.ObjectData(c, gin.H{"error": wrapped.Error}); writeErr != nil {
				logger.Errorf(c.Request.Context(), "write CommandCode stream error: %s", writeErr)
			}
		}
		return wrapped, text.String(), nil
	}
	process := func(data string) (bool, error) {
		if data == "[DONE]" {
			if len(choices) == 0 {
				return false, errors.New("CommandCode stream ended without a completion")
			}
			for _, finished := range choices {
				if !finished {
					return false, errors.New("CommandCode stream ended without finish_reason")
				}
			}
			if usage == nil {
				return false, errors.New("CommandCode stream ended without usage")
			}
			common.SetEventStreamHeaders(c)
			render.Done(c)
			if body, ok := resp.Body.(*commandCodeBody); ok {
				if err := body.drainForReuse(); err != nil {
					logger.Debugf(c.Request.Context(), "CommandCode completed response not drained: %s", err)
				}
			}
			return true, nil
		}
		var envelope struct {
			Error json.RawMessage `json:"error"`
			ChatCompletionsStreamResponse
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return false, fmt.Errorf("invalid CommandCode stream event: %w", err)
		}
		if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
			return false, fmt.Errorf("CommandCode upstream stream error: %s", envelope.Error)
		}
		if envelope.Usage != nil {
			usage = envelope.Usage
		}
		for _, choice := range envelope.Choices {
			choices[choice.Index] = choices[choice.Index] || (choice.FinishReason != nil && *choice.FinishReason != "")
			if content, ok := choice.Delta.Content.(string); ok {
				text.WriteString(content)
			}
		}
		if mode == relaymode.Completions {
			var completion CompletionsStreamResponse
			if err := json.Unmarshal([]byte(data), &completion); err != nil {
				return false, err
			}
			for _, choice := range completion.Choices {
				text.WriteString(choice.Text)
			}
		}
		common.SetEventStreamHeaders(c)
		render.StringData(c, "data: "+data)
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(event) == 0 {
				continue
			}
			finished, err := process(strings.Join(event, "\n"))
			event = nil
			eventBytes = 0
			if err != nil {
				return fail(err, "commandcode_stream_error")
			}
			if finished {
				return nil, text.String(), usage
			}
		} else if strings.HasPrefix(line, "data:") {
			eventBytes += len(line)
			if eventBytes > 4*1024*1024 {
				return fail(errors.New("CommandCode stream event exceeds 4 MiB"), "commandcode_stream_error")
			}
			event = append(event, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(err, "commandcode_stream_read_failed")
	}
	if len(event) > 0 {
		finished, err := process(strings.Join(event, "\n"))
		if err != nil {
			return fail(err, "commandcode_stream_error")
		}
		if finished {
			return nil, text.String(), usage
		}
	}
	return fail(io.ErrUnexpectedEOF, "commandcode_stream_incomplete")
}
