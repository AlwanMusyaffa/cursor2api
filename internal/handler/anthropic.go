// Package handler 提供 HTTP 请求处理器
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cursor2api/internal/browser"
	"cursor2api/internal/toolify"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ================== 请求/响应结构体 ==================

// MessagesRequest Anthropic Messages API 请求格式
type MessagesRequest struct {
	Model     string                   `json:"model"`
	Messages  []Message                `json:"messages"`
	MaxTokens int                      `json:"max_tokens"`
	Stream    bool                     `json:"stream"`
	System    interface{}              `json:"system,omitempty"` // 可以是 string 或 []ContentBlock
	Tools     []toolify.ToolDefinition `json:"tools,omitempty"`
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
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`    // tool_use
	Name  string                 `json:"name,omitempty"`  // tool_use
	Input map[string]interface{} `json:"input,omitempty"` // tool_use
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
	// 直接透传模型名称，不做转换
	if model == "" {
		return "claude-opus-4-5-20251101"
	}
	return model
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
		handleStream(c, cursorReq, req.Model, req.Tools)
	} else {
		handleNonStream(c, cursorReq, req.Model, req.Tools)
	}
}

// ================== 请求转换 ==================

// convertToCursor 将 Anthropic 请求转换为 Cursor 格式
func convertToCursor(req MessagesRequest) browser.CursorChatRequest {
	messages := make([]browser.CursorMessage, 0, len(req.Messages)+1)

	// 构建系统消息
	sysText := getTextContent(req.System)
	if sysText != "" {
		messages = append(messages, browser.CursorMessage{
			Parts: []browser.CursorPart{{Type: "text", Text: sysText}},
			ID:    generateID(),
			Role:  "system",
		})
	}

	// 检测是否有 tool_result（表示工具已执行过）
	hasToolResult := false
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			if content, ok := msg.Content.([]interface{}); ok {
				for _, item := range content {
					if block, ok := item.(map[string]interface{}); ok {
						if block["type"] == "tool_result" {
							hasToolResult = true
							break
						}
					}
				}
			}
		}
	}

	// 只有第一次调用时才注入工具提示（没有 tool_result）
	toolPrompt := ""
	if len(req.Tools) > 0 && !hasToolResult {
		toolPrompt = toolify.GenerateToolPrompt(req.Tools)
	}

	// 添加用户/助手消息
	firstUserMsg := true
	for _, msg := range req.Messages {
		text := extractMessageText(msg)
		if text != "" {
			// 把工具提示放在第一条用户消息前面
			if msg.Role == "user" && firstUserMsg && toolPrompt != "" {
				text = toolPrompt + "\n\n" + text
				firstUserMsg = false
			}
			messages = append(messages, browser.CursorMessage{
				Parts: []browser.CursorPart{{Type: "text", Text: text}},
				ID:    generateID(),
				Role:  msg.Role,
			})
		}
	}

	return browser.CursorChatRequest{
		Model:    mapModelName(req.Model),
		ID:       generateID(),
		Messages: messages,
		Trigger:  "submit-message",
		Tools:    req.Tools, // 透传工具定义
	}
}

// extractMessageText 从消息中提取文本
func extractMessageText(msg Message) string {
	content := msg.Content
	if content == nil {
		return ""
	}

	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var texts []string
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				if text, ok := block["text"].(string); ok {
					texts = append(texts, text)
				}
			case "tool_result":
				// 提取 tool_result 内容
				toolID := ""
				if id, ok := block["tool_use_id"].(string); ok {
					toolID = id
				}
				resultContent := ""
				if c, ok := block["content"].(string); ok {
					resultContent = c
				} else if c, ok := block["content"].([]interface{}); ok {
					for _, item := range c {
						if b, ok := item.(map[string]interface{}); ok {
							if b["type"] == "text" {
								if t, ok := b["text"].(string); ok {
									resultContent += t
								}
							}
						}
					}
				}
				texts = append(texts, fmt.Sprintf("[Tool %s result]: %s", toolID, resultContent))
			}
		}
		return strings.Join(texts, "\n")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ================== API 处理 ==================

// handleStream 处理流式请求
func handleStream(c *gin.Context, cursorReq browser.CursorChatRequest, model string, tools []toolify.ToolDefinition) {
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

	var buffer, fullResponse strings.Builder
	blockIndex := 0
	toolCount := 0

	// 发送工具调用的辅助函数
	sendToolCall := func(toolName, argsJSON string) {
		toolID := fmt.Sprintf("toolu_%d", toolCount)
		toolCount++

		var args map[string]interface{}
		json.Unmarshal([]byte(argsJSON), &args)
		inputJSON, _ := json.Marshal(args)
		partialJSONStr, _ := json.Marshal(string(inputJSON))

		c.Writer.WriteString("event: content_block_start\n")
		c.Writer.WriteString(fmt.Sprintf(`data: {"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":"%s","name":"%s","input":{}}}`+"\n\n", blockIndex, toolID, toolName))
		c.Writer.WriteString("event: content_block_delta\n")
		c.Writer.WriteString(fmt.Sprintf(`data: {"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`+"\n\n", blockIndex, string(partialJSONStr)))
		c.Writer.WriteString("event: content_block_stop\n")
		c.Writer.WriteString(fmt.Sprintf(`data: {"type":"content_block_stop","index":%d}`+"\n\n", blockIndex))
		blockIndex++
		flusher.Flush()
	}

	svc := browser.GetService()
	err := svc.SendStreamRequest(cursorReq, func(chunk string) {
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
			if data == "" {
				continue
			}

			var event CursorSSEEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			if event.Type == "text-delta" && event.Delta != "" {
				fullResponse.WriteString(event.Delta)
				// 只累积，不实时解析（避免重复执行）
			}
		}
	})

	if err != nil {
		c.Writer.WriteString("event: error\n")
		c.Writer.WriteString(`data: {"type":"error","error":{"message":"` + err.Error() + `"}}` + "\n\n")
		flusher.Flush()
		return
	}

	// 解析完整响应
	responseText := fullResponse.String()
	toolCalls, cleanText := toolify.ParseToolCalls(responseText)

	// 发送干净文本
	if cleanText != "" {
		textJSON, _ := json.Marshal(cleanText)
		c.Writer.WriteString("event: content_block_start\n")
		c.Writer.WriteString(fmt.Sprintf(`data: {"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`+"\n\n", blockIndex))
		c.Writer.WriteString("event: content_block_delta\n")
		c.Writer.WriteString(fmt.Sprintf(`data: {"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`+"\n\n", blockIndex, string(textJSON)))
		c.Writer.WriteString("event: content_block_stop\n")
		c.Writer.WriteString(fmt.Sprintf(`data: {"type":"content_block_stop","index":%d}`+"\n\n", blockIndex))
		flusher.Flush()
		blockIndex++
	}

	// 发送工具调用
	stopReason := "end_turn"
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
		for _, call := range toolCalls {
			sendToolCall(call.Function.Name, call.Function.Arguments)
		}
	}

	c.Writer.WriteString("event: message_delta\n")
	c.Writer.WriteString(fmt.Sprintf(`data: {"type":"message_delta","delta":{"stop_reason":"%s","stop_sequence":null},"usage":{"output_tokens":100}}`+"\n\n", stopReason))
	c.Writer.WriteString("event: message_stop\n")
	c.Writer.WriteString(`data: {"type":"message_stop"}` + "\n\n")
	flusher.Flush()
}

// handleNonStream 处理非流式请求
func handleNonStream(c *gin.Context, cursorReq browser.CursorChatRequest, model string, tools []toolify.ToolDefinition) {
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

	responseText := fullText.String()
	var contentBlocks []ContentBlock
	stopReason := "end_turn"

	// 检测工具调用
	if len(tools) > 0 {
		toolCalls, cleanText := toolify.ParseToolCalls(responseText)
		if len(toolCalls) > 0 {
			stopReason = "tool_use"
			if cleanText != "" {
				contentBlocks = append(contentBlocks, ContentBlock{Type: "text", Text: cleanText})
			}
			for _, call := range toolCalls {
				var args map[string]interface{}
				json.Unmarshal([]byte(call.Function.Arguments), &args)
				contentBlocks = append(contentBlocks, ContentBlock{
					Type:  "tool_use",
					ID:    "toolu_" + call.ID,
					Name:  call.Function.Name,
					Input: args,
				})
			}
		} else {
			contentBlocks = append(contentBlocks, ContentBlock{Type: "text", Text: responseText})
		}
	} else {
		contentBlocks = append(contentBlocks, ContentBlock{Type: "text", Text: responseText})
	}

	c.JSON(http.StatusOK, MessagesResponse{
		ID:         "msg_" + generateID(),
		Type:       "message",
		Role:       "assistant",
		Content:    contentBlocks,
		Model:      model,
		StopReason: stopReason,
		Usage:      Usage{InputTokens: 100, OutputTokens: 100},
	})
}
