// Package config 提供配置文件加载和管理功能
package config

import (
	"log"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config 应用配置结构
type Config struct {
	// Port 服务监听端口
	Port string `yaml:"port"`
	// Browser 浏览器相关配置
	Browser BrowserConfig `yaml:"browser"`
}

// BrowserConfig 浏览器配置
type BrowserConfig struct {
	// Headless 是否使用无头模式
	Headless bool `yaml:"headless"`
	// Path Chromium 可执行文件路径
	Path string `yaml:"path"`
}

var (
	cfg  *Config
	once sync.Once
)

// Get 获取全局配置实例（单例模式）
func Get() *Config {
	once.Do(func() {
		cfg = &Config{
			Port: "3010",
			Browser: BrowserConfig{
				Headless: true,
				Path:     "/usr/bin/chromium",
			},
		}
		load(cfg)
	})
	return cfg
}

// load 从配置文件和环境变量加载配置
func load(c *Config) {
	// 尝试读取 YAML 配置文件
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Printf("[配置] 未找到 config.yaml，使用默认配置")
	} else {
		if err := yaml.Unmarshal(data, c); err != nil {
			log.Printf("[配置] 解析 config.yaml 失败: %v", err)
		} else {
			log.Printf("[配置] 已加载 config.yaml")
		}
	}

	// 环境变量覆盖配置文件
	if port := os.Getenv("PORT"); port != "" {
		c.Port = port
	}

	// 输出最终配置
	log.Printf("[配置] 端口: %s", c.Port)
}
