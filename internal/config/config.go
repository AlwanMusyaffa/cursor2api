// Package config 提供配置文件加载和管理功能
package config

import (
	"log"
	"os"
	"runtime"
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
	// Path Chromium 可执行文件路径，留空则自动下载
	Path string `yaml:"path"`
	// AutoExecute 当 AI 拒绝执行时自动提取并执行命令
	AutoExecute bool `yaml:"auto_execute"`
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
				Headless:    true,
				Path:        "",   // 留空表示自动检测或下载
				AutoExecute: true, // 默认开启自动执行
			},
		}
		load(cfg)
	})
	return cfg
}

// detectBrowserPath 自动检测系统中已安装的浏览器路径
func detectBrowserPath() string {
	var paths []string

	switch runtime.GOOS {
	case "darwin": // macOS
		paths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "linux":
		paths = []string{
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/snap/bin/chromium",
		}
	case "windows":
		paths = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "" // 返回空，让 go-rod 自动下载
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
	if browserPath := os.Getenv("BROWSER_PATH"); browserPath != "" {
		c.Browser.Path = browserPath
	}
	if autoExec := os.Getenv("AUTO_EXECUTE"); autoExec != "" {
		c.Browser.AutoExecute = autoExec == "true" || autoExec == "1"
	}

	// 如果浏览器路径未指定，尝试自动检测
	if c.Browser.Path == "" {
		c.Browser.Path = detectBrowserPath()
		if c.Browser.Path != "" {
			log.Printf("[配置] 自动检测到浏览器: %s", c.Browser.Path)
		} else {
			log.Printf("[配置] 未检测到浏览器，将使用 go-rod 自动下载")
		}
	}

	// 输出最终配置
	log.Printf("[配置] 端口: %s", c.Port)
}
