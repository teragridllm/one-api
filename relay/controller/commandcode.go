package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
)

func commandCodeRequestBody(c *gin.Context, relayMeta *meta.Meta, request *model.GeneralOpenAIRequest) (io.Reader, error) {
	raw, err := common.GetRequestBody(c)
	if err != nil {
		return nil, fmt.Errorf("read CommandCode request: %w", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("parse CommandCode request: %w", err)
	}
	if body == nil {
		return nil, fmt.Errorf("CommandCode request must be an object")
	}
	body["model"], err = json.Marshal(request.Model)
	if err != nil {
		return nil, err
	}
	if request.Stream {
		options := make(map[string]json.RawMessage)
		if rawOptions, ok := body["stream_options"]; ok && string(rawOptions) != "null" {
			if err := json.Unmarshal(rawOptions, &options); err != nil {
				return nil, fmt.Errorf("parse CommandCode stream options: %w", err)
			}
		}
		options["include_usage"] = json.RawMessage("true")
		body["stream_options"], err = json.Marshal(options)
		if err != nil {
			return nil, err
		}
	}
	if relayMeta.ForcedSystemPrompt != "" {
		var messages []map[string]json.RawMessage
		if err := json.Unmarshal(body["messages"], &messages); err != nil {
			return nil, fmt.Errorf("parse CommandCode messages: %w", err)
		}
		prompt, err := json.Marshal(relayMeta.ForcedSystemPrompt)
		if err != nil {
			return nil, err
		}
		var firstRole string
		if len(messages) > 0 {
			if err := json.Unmarshal(messages[0]["role"], &firstRole); err != nil {
				return nil, fmt.Errorf("parse CommandCode message role: %w", err)
			}
		}
		if firstRole == "system" {
			messages[0]["content"] = prompt
		} else {
			messages = append([]map[string]json.RawMessage{{"role": json.RawMessage(`"system"`), "content": prompt}}, messages...)
		}
		body["messages"], err = json.Marshal(messages)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode CommandCode request: %w", err)
	}
	return bytes.NewReader(encoded), nil
}
