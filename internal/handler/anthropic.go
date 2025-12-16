// Package handler 提供 HTTP 请求处理器
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cursor2api/internal/browser"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ================== 请求/响应结构体 ==================

// MessagesRequest Anthropic Messages API 请求格式
type MessagesRequest struct {
	Model     string      `json:"model"`
	Messages  []Message   `json:"messages"`
	MaxTokens int         `json:"max_tokens"`
	Stream    bool        `json:"stream"`
	System    interface{} `json:"system,omitempty"` // 可以是 string 或 []ContentBlock
}

// Message 消息格式
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // 可以是 string 或 []ContentBlock
}

// MessagesResponse Anthropic Messages API 响应格式
type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

// ContentBlock 内容块
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Usage token 使用统计
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// CursorSSEEvent Cursor SSE 事件格式
type CursorSSEEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

// ================== 辅助函数 ==================

// generateID 生成唯一标识符
func generateID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
}

// getTextContent 从 interface{} 提取文本内容
// 支持 string 和 []ContentBlock 两种格式
func getTextContent(content interface{}) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var texts []string
		for _, item := range v {
			if block, ok := item.(map[string]interface{}); ok {
				if block["type"] == "text" {
					if text, ok := block["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		return strings.Join(texts, "\n")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// mapModelName 将模型名称映射到 Cursor 支持的格式
func mapModelName(model string) string {
	model = strings.ToLower(model)

	// 已经是 Cursor 格式
	if strings.Contains(model, "/") {
		return model
	}

	// Claude 模型
	if strings.Contains(model, "claude") {
		return "anthropic/claude-sonnet-4.5"
	}

	// GPT 模型
	if strings.Contains(model, "gpt") {
		return "openai/gpt-5-nano"
	}

	// Gemini 模型
	if strings.Contains(model, "gemini") {
		return "google/gemini-2.5-flash"
	}

	// 默认使用 Claude
	return "anthropic/claude-sonnet-4.5"
}

// ================== 处理器函数 ==================

// CountTokens 估算 token 数量
func CountTokens(c *gin.Context) {
	var req MessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	// 简单估算：每 4 个字符约 1 个 token
	totalChars := len(getTextContent(req.System))
	for _, msg := range req.Messages {
		totalChars += len(getTextContent(msg.Content))
	}
	tokens := totalChars / 4
	if tokens < 1 {
		tokens = 1
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": tokens})
}

// Messages 处理 Anthropic Messages API 请求
func Messages(c *gin.Context) {
	var req MessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	// 转换为 Cursor 请求格式
	cursorReq := convertToCursor(req)

	if req.Stream {
		handleStream(c, cursorReq, req.Model)
	} else {
		handleNonStream(c, cursorReq, req.Model)
	}
}

// ================== 请求转换 ==================

// convertToCursor 将 Anthropic 请求转换为 Cursor 格式
func convertToCursor(req MessagesRequest) browser.CursorChatRequest {
	messages := make([]browser.CursorMessage, 0, len(req.Messages)+1)

	// 添加 system 消息
	if sysText := getTextContent(req.System); sysText != "" {
		messages = append(messages, browser.CursorMessage{
			Parts: []browser.CursorPart{{Type: "text", Text: sysText}},
			ID:    generateID(),
			Role:  "system",
		})
	}

	// 添加用户/助手消息
	for _, msg := range req.Messages {
		messages = append(messages, browser.CursorMessage{
			Parts: []browser.CursorPart{{Type: "text", Text: getTextContent(msg.Content)}},
			ID:    generateID(),
			Role:  msg.Role,
		})
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

// ================== API 处理 ==================

// handleStream 处理流式请求
func handleStream(c *gin.Context, cursorReq browser.CursorChatRequest, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, _ := c.Writer.(http.Flusher)
	id := "msg_" + generateID()

	// 发送 message_start
	c.Writer.WriteString("event: message_start\n")
	c.Writer.WriteString(fmt.Sprintf(`data: {"type":"message_start","message":{"id":"%s","type":"message","role":"assistant","content":[],"model":"%s","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":0}}}`+"\n\n", id, model))
	flusher.Flush()

	c.Writer.WriteString("event: content_block_start\n")
	c.Writer.WriteString(`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n")
	flusher.Flush()

	// 用于累积不完整的 SSE 行
	var buffer strings.Builder

	svc := browser.GetService()
	err := svc.SendStreamRequest(cursorReq, func(chunk string) {
		buffer.WriteString(chunk)
		content := buffer.String()
		lines := strings.Split(content, "\n")

		// 保留最后一个可能不完整的行
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
			if data == "" {
				continue
			}

			var event CursorSSEEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			if event.Type == "text-delta" && event.Delta != "" {
				deltaJSON, _ := json.Marshal(event.Delta)
				c.Writer.WriteString("event: content_block_delta\n")
				c.Writer.WriteString(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + string(deltaJSON) + `}}` + "\n\n")
				flusher.Flush()
			}
		}
	})

	if err != nil {
		c.Writer.WriteString("event: error\n")
		c.Writer.WriteString(`data: {"type":"error","error":{"message":"` + err.Error() + `"}}` + "\n\n")
		flusher.Flush()
	}

	c.Writer.WriteString("event: content_block_stop\n")
	c.Writer.WriteString(`data: {"type":"content_block_stop","index":0}` + "\n\n")
	flusher.Flush()

	c.Writer.WriteString("event: message_delta\n")
	c.Writer.WriteString(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":100}}` + "\n\n")
	flusher.Flush()

	c.Writer.WriteString("event: message_stop\n")
	c.Writer.WriteString(`data: {"type":"message_stop"}` + "\n\n")
	flusher.Flush()
}

// handleNonStream 处理非流式请求
func handleNonStream(c *gin.Context, cursorReq browser.CursorChatRequest, model string) {
	svc := browser.GetService()
	result, err := svc.SendRequest(cursorReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	// 解析响应
	var fullText strings.Builder
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var event CursorSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "text-delta" && event.Delta != "" {
			fullText.WriteString(event.Delta)
		}
	}

	c.JSON(http.StatusOK, MessagesResponse{
		ID:         "msg_" + generateID(),
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: fullText.String()}},
		Model:      model,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: 100, OutputTokens: 100},
	})
}
