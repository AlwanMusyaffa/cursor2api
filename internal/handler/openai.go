// Package handler 提供 HTTP 请求处理器
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cursor2api/internal/browser"

	"github.com/gin-gonic/gin"
)

// ================== OpenAI 兼容格式 ==================

// ChatCompletionRequest OpenAI Chat Completion 请求格式
type ChatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

// OpenAIMessage OpenAI 消息格式
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionResponse OpenAI Chat Completion 响应格式
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []Choice     `json:"choices"`
	Usage   *OpenAIUsage `json:"usage,omitempty"`
}

// Choice 选项
type Choice struct {
	Index        int            `json:"index"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	Delta        *OpenAIMessage `json:"delta,omitempty"`
	FinishReason *string        `json:"finish_reason"`
}

// OpenAIUsage token 使用统计
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionChunk 流式响应块
type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
}

// ChunkChoice 流式选项
type ChunkChoice struct {
	Index        int           `json:"index"`
	Delta        OpenAIMessage `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

// ================== 处理器函数 ==================

// ChatCompletions 处理 OpenAI Chat Completions API 请求
func ChatCompletions(c *gin.Context) {
	var req ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[OpenAI] 请求: model=%s, messages=%d, stream=%v", req.Model, len(req.Messages), req.Stream)

	// 转换为 Cursor 请求格式
	cursorReq := convertOpenAIToCursor(req)

	if req.Stream {
		handleOpenAIStream(c, cursorReq, req.Model)
	} else {
		handleOpenAINonStream(c, cursorReq, req.Model)
	}
}

// convertOpenAIToCursor 将 OpenAI 请求转换为 Cursor 格式
func convertOpenAIToCursor(req ChatCompletionRequest) browser.CursorChatRequest {
	messages := make([]browser.CursorMessage, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = browser.CursorMessage{
			Parts: []browser.CursorPart{{Type: "text", Text: msg.Content}},
			ID:    generateID(),
			Role:  msg.Role,
		}
	}

	return browser.CursorChatRequest{
		Context: []browser.CursorContext{{
			Type:     "file",
			Content:  "",
			FilePath: "/docs/",
		}},
		Model:    mapModelName(req.Model),
		ID:       generateID(),
		Messages: messages,
		Trigger:  "submit-message",
	}
}

// handleOpenAIStream 处理 OpenAI 流式请求
func handleOpenAIStream(c *gin.Context, cursorReq browser.CursorChatRequest, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	id := "chatcmpl-" + generateID()
	created := time.Now().Unix()
	flusher, _ := c.Writer.(http.Flusher)

	var buffer strings.Builder

	svc := browser.GetService()
	_ = svc.SendStreamRequest(cursorReq, func(chunk string) {
		buffer.WriteString(chunk)
		content := buffer.String()
		lines := strings.Split(content, "\n")

		if !strings.HasSuffix(content, "\n") && len(lines) > 0 {
			buffer.Reset()
			buffer.WriteString(lines[len(lines)-1])
			lines = lines[:len(lines)-1]
		} else {
			buffer.Reset()
		}

		for _, line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" || data == "[DONE]" {
				continue
			}

			var event CursorSSEEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			if event.Type == "text-delta" && event.Delta != "" {
				chunk := ChatCompletionChunk{
					ID:      id,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []ChunkChoice{{
						Index: 0,
						Delta: OpenAIMessage{Content: event.Delta},
					}},
				}
				chunkJSON, _ := json.Marshal(chunk)
				c.Writer.Write([]byte(fmt.Sprintf("data: %s\n\n", chunkJSON)))
				flusher.Flush()
			}
		}
	})

	// 发送结束标记
	reason := "stop"
	endChunk := ChatCompletionChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []ChunkChoice{{
			Index:        0,
			Delta:        OpenAIMessage{},
			FinishReason: &reason,
		}},
	}
	endJSON, _ := json.Marshal(endChunk)
	c.Writer.Write([]byte(fmt.Sprintf("data: %s\n\n", endJSON)))
	c.Writer.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}

// handleOpenAINonStream 处理 OpenAI 非流式请求
func handleOpenAINonStream(c *gin.Context, cursorReq browser.CursorChatRequest, model string) {
	svc := browser.GetService()
	result, err := svc.SendRequest(cursorReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 解析响应
	var fullContent strings.Builder
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" || data == "[DONE]" {
			continue
		}

		var event CursorSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "text-delta" {
			fullContent.WriteString(event.Delta)
		}
	}

	reason := "stop"
	c.JSON(http.StatusOK, ChatCompletionResponse{
		ID:      "chatcmpl-" + generateID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{{
			Index:        0,
			Message:      &OpenAIMessage{Role: "assistant", Content: fullContent.String()},
			FinishReason: &reason,
		}},
		Usage: &OpenAIUsage{
			PromptTokens:     100,
			CompletionTokens: 100,
			TotalTokens:      200,
		},
	})
}
