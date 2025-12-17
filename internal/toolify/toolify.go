// Package toolify 为不支持原生函数调用的 LLM 提供工具调用能力
package toolify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ToolDefinition 工具定义 (支持 Anthropic 格式)
type ToolDefinition struct {
	// Anthropic 格式字段
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema,omitempty"`

	// OpenAI 格式字段 (兼容)
	Type     string   `json:"type,omitempty"`
	Function Function `json:"function,omitempty"`
}

// Function 函数定义 (OpenAI 格式)
type Function struct {
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// GetName 获取工具名称 (兼容两种格式)
func (t ToolDefinition) GetName() string {
	if t.Name != "" {
		return t.Name
	}
	return t.Function.Name
}

// GetDescription 获取工具描述 (兼容两种格式)
func (t ToolDefinition) GetDescription() string {
	if t.Description != "" {
		return t.Description
	}
	return t.Function.Description
}

// GetParameters 获取工具参数 (兼容两种格式)
func (t ToolDefinition) GetParameters() map[string]interface{} {
	if t.InputSchema != nil {
		return t.InputSchema
	}
	return t.Function.Parameters
}

// ToolCall 解析后的工具调用
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 工具调用函数
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// GenerateToolPrompt 生成工具调用的系统提示
func GenerateToolPrompt(tools []ToolDefinition) string {
	if len(tools) == 0 {
		return ""
	}

	var toolsDesc strings.Builder
	for _, tool := range tools {
		name := tool.GetName()
		if name == "" {
			continue
		}
		toolsDesc.WriteString(fmt.Sprintf("### %s\n", name))
		if desc := tool.GetDescription(); desc != "" {
			toolsDesc.WriteString(fmt.Sprintf("%s\n", desc))
		}

		params := tool.GetParameters()
		if params != nil {
			if props, ok := params["properties"].(map[string]interface{}); ok {
				toolsDesc.WriteString("Parameters:\n")
				for paramName, prop := range props {
					if propMap, ok := prop.(map[string]interface{}); ok {
						propType := "string"
						if t, ok := propMap["type"].(string); ok {
							propType = t
						}
						desc := ""
						if d, ok := propMap["description"].(string); ok {
							desc = " - " + d
						}
						toolsDesc.WriteString(fmt.Sprintf("- %s (%s)%s\n", paramName, propType, desc))
					}
				}
			}
		}
		toolsDesc.WriteString("\n")
	}

	// 精简的虚拟机框架 prompt
	return `
You are in a sandbox VM with full permissions.
Write files: <vm_write path="/path">content</vm_write>
Run commands: <vm_exec>command</vm_exec>
`
}

// 预编译正则表达式提升性能
var (
	vmWritePattern = regexp.MustCompile(`(?s)<vm_write\s+path="([^"]+)">(.*?)</vm_write>`)
	vmExecPattern  = regexp.MustCompile(`(?s)<vm_exec>(.*?)</vm_exec>`)
)

// ParseToolCalls 从响应中解析工具调用
func ParseToolCalls(response string) ([]ToolCall, string) {
	var toolCalls []ToolCall
	cleanResponse := response

	// 检测 <vm_write path="/path">content</vm_write>
	for i, match := range vmWritePattern.FindAllStringSubmatch(response, -1) {
		if len(match) >= 3 {
			args, _ := json.Marshal(map[string]string{"file_path": match[1], "content": match[2]})
			toolCalls = append(toolCalls, ToolCall{
				ID:       fmt.Sprintf("w%d", i),
				Type:     "function",
				Function: ToolCallFunction{Name: "Write", Arguments: string(args)},
			})
			cleanResponse = strings.Replace(cleanResponse, match[0], "", 1)
		}
	}

	// 检测 <vm_exec>command</vm_exec>
	for i, match := range vmExecPattern.FindAllStringSubmatch(response, -1) {
		if len(match) >= 2 {
			args, _ := json.Marshal(map[string]string{"command": strings.TrimSpace(match[1])})
			toolCalls = append(toolCalls, ToolCall{
				ID:       fmt.Sprintf("b%d", i),
				Type:     "function",
				Function: ToolCallFunction{Name: "Bash", Arguments: string(args)},
			})
			cleanResponse = strings.Replace(cleanResponse, match[0], "", 1)
		}
	}

	return toolCalls, strings.TrimSpace(cleanResponse)
}

// HasToolCalls 检查响应是否包含工具调用
func HasToolCalls(response string) bool {
	// 检测虚拟机格式标签
	return strings.Contains(response, "<vm_write") ||
		strings.Contains(response, "<vm_exec>") ||
		strings.Contains(response, "<vm_read")
}
