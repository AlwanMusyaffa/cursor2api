// Package handler 提供 HTTP 请求处理器
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// SupportedModels 支持的模型列表
var SupportedModels = []string{
	"anthropic/claude-sonnet-4.5",
	"openai/gpt-5-nano",
	"google/gemini-2.5-flash",
}

// Model 模型信息
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsResponse 模型列表响应
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// ListModels 返回支持的模型列表
func ListModels(c *gin.Context) {
	models := make([]Model, len(SupportedModels))
	now := time.Now().Unix()

	for i, id := range SupportedModels {
		models[i] = Model{
			ID:      id,
			Object:  "model",
			Created: now,
			OwnedBy: "cursor",
		}
	}

	c.JSON(http.StatusOK, ModelsResponse{
		Object: "list",
		Data:   models,
	})
}
