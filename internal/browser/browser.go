// Package browser 提供基于 Chromium 的浏览器自动化服务
// 用于绕过 Cursor API 的人机验证（X-Is-Human token）
package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"cursor2api/internal/config"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

// Service 浏览器服务，管理浏览器实例和请求
type Service struct {
	browser   *rod.Browser // 浏览器实例
	page      *rod.Page    // 当前页面
	xIsHuman  string       // X-Is-Human token
	mu        sync.RWMutex // 读写锁
	lastFetch time.Time    // 上次获取 token 时间
}

var (
	instance *Service
	once     sync.Once
)

// GetService 获取浏览器服务单例
func GetService() *Service {
	once.Do(func() {
		instance = &Service{}
		instance.init()
	})
	return instance
}

// init 初始化浏览器实例
func (s *Service) init() {
	cfg := config.Get()

	// 创建临时用户数据目录
	userDataDir := os.Getenv("BROWSER_USER_DATA_DIR")
	if userDataDir == "" {
		userDataDir = fmt.Sprintf("/tmp/cursor2api-browser-%d", time.Now().UnixNano())
	}

	// 配置浏览器启动参数
	l := launcher.New().
		Bin(cfg.Browser.Path).
		Headless(cfg.Browser.Headless).
		Set("disable-blink-features", "AutomationControlled"). // 隐藏自动化特征
		Set("no-sandbox").
		Set("disable-gpu").
		Set("disable-dev-shm-usage").
		Set("no-proxy-server"). // 浏览器不使用代理
		UserDataDir(userDataDir)

	u := l.MustLaunch()

	s.browser = rod.New().ControlURL(u).MustConnect()
}

// RefreshToken 刷新 X-Is-Human token
func (s *Service) RefreshToken() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 关闭旧页面
	if s.page != nil {
		s.page.Close()
	}

	s.page = s.browser.MustPage()

	// 设置 User-Agent
	s.page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
	})

	// 隐藏 webdriver 特征
	s.page.MustEvalOnNewDocument(`Object.defineProperty(navigator, 'webdriver', {get: () => false})`)

	// 监听请求，捕获 token
	var capturedToken string
	router := s.page.HijackRequests()

	router.MustAdd("*/api/chat*", func(ctx *rod.Hijack) {
		headers := ctx.Request.Headers()
		if token, ok := headers["x-is-human"]; ok {
			capturedToken = token.String()
		}
		ctx.ContinueRequest(&proto.FetchContinueRequest{})
	})

	go router.Run()

	// 访问 Cursor 文档页面
	if err := s.page.Navigate("https://cursor.com/cn/docs"); err != nil {
		router.Stop()
		return fmt.Errorf("导航失败: %w", err)
	}

	s.page.MustWaitLoad()
	time.Sleep(5 * time.Second)

	// 尝试触发聊天请求
	askBtn, err := s.page.Timeout(10 * time.Second).Element(`button:has-text("询问"), button:has-text("Ask"), [data-testid="ask-ai"]`)
	if err != nil {
		askBtn, err = s.page.Timeout(5 * time.Second).Element(`textarea, input[type="text"]`)
	}

	if askBtn != nil {
		askBtn.Click(proto.InputMouseButtonLeft, 1)
		time.Sleep(1 * time.Second)
		askBtn.Input("hi")
		time.Sleep(500 * time.Millisecond)
		s.page.Keyboard.Press(13)
	}

	// 等待请求被捕获
	time.Sleep(8 * time.Second)
	router.Stop()

	if capturedToken != "" {
		s.xIsHuman = capturedToken
		s.lastFetch = time.Now()
	}

	return nil
}

// GetXIsHuman 获取当前 X-Is-Human token
func (s *Service) GetXIsHuman() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Token 过期检查（30 分钟刷新）
	if time.Since(s.lastFetch) > 30*time.Minute && s.xIsHuman != "" {
		go s.RefreshToken()
	}

	return s.xIsHuman
}

// CursorChatRequest Cursor API 请求格式
type CursorChatRequest struct {
	Context  []CursorContext `json:"context"`
	Model    string          `json:"model"`
	ID       string          `json:"id"`
	Messages []CursorMessage `json:"messages"`
	Trigger  string          `json:"trigger"`
}

// CursorContext 上下文信息
type CursorContext struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	FilePath string `json:"filePath"`
}

// CursorMessage 消息格式
type CursorMessage struct {
	Parts []CursorPart `json:"parts"`
	ID    string       `json:"id"`
	Role  string       `json:"role"`
}

// CursorPart 消息内容
type CursorPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// SendRequest 发送聊天请求（非流式）
func (s *Service) SendRequest(req CursorChatRequest) (string, error) {
	if s.browser == nil {
		return "", fmt.Errorf("浏览器未初始化")
	}

	// 创建新页面
	page := s.browser.MustPage()
	defer page.Close()

	// 设置浏览器特征
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
	})
	page.MustEvalOnNewDocument(`Object.defineProperty(navigator, 'webdriver', {get: () => false})`)

	// 导航到 Cursor
	page.MustNavigate("https://cursor.com/cn/docs").MustWaitLoad()

	reqJSON, _ := json.Marshal(req)

	// 使用 JavaScript 发送请求
	script := fmt.Sprintf(`() => {
		return new Promise((resolve, reject) => {
			fetch('https://cursor.com/api/chat', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(%s)
			})
			.then(response => {
				if (!response.ok) {
					return response.text().then(text => reject(new Error(text)));
				}
				return response.text();
			})
			.then(text => resolve(text))
			.catch(err => reject(err));
		});
	}`, string(reqJSON))

	result, err := page.Timeout(90 * time.Second).Evaluate(rod.Eval(script).ByPromise())
	if err != nil {
		return "", fmt.Errorf("执行失败: %w", err)
	}

	return result.Value.String(), nil
}

// SendStreamRequest 发送流式聊天请求
func (s *Service) SendStreamRequest(req CursorChatRequest, onChunk func(chunk string)) error {
	if s.browser == nil {
		return fmt.Errorf("浏览器未初始化")
	}

	// 创建新页面
	page := s.browser.MustPage()
	defer page.Close()

	// 设置浏览器特征
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
	})
	page.MustEvalOnNewDocument(`Object.defineProperty(navigator, 'webdriver', {get: () => false})`)

	// 暴露回调函数给 JavaScript
	done := make(chan error, 1)
	page.MustExpose("goStreamCallback", func(j gson.JSON) (interface{}, error) {
		onChunk(j.String())
		return "ok", nil
	})
	page.MustExpose("goStreamDone", func(j gson.JSON) (interface{}, error) {
		errMsg := j.String()
		if errMsg != "" {
			done <- fmt.Errorf("%s", errMsg)
		} else {
			done <- nil
		}
		return "ok", nil
	})

	// 导航到 Cursor
	page.MustNavigate("https://cursor.com/cn/docs").MustWaitLoad()

	reqJSON, _ := json.Marshal(req)

	// 使用 JavaScript 发送流式请求
	script := fmt.Sprintf(`() => {
		fetch('https://cursor.com/api/chat', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(%s)
		})
		.then(response => {
			if (!response.ok) {
				return response.text().then(text => {
					window.goStreamDone(text);
					throw new Error(text);
				});
			}
			const reader = response.body.getReader();
			const decoder = new TextDecoder();
			
			function read() {
				reader.read().then(({done, value}) => {
					if (done) {
						window.goStreamDone("");
						return;
					}
					const chunk = decoder.decode(value, {stream: true});
					window.goStreamCallback(chunk);
					read();
				}).catch(err => {
					window.goStreamDone(err.message);
				});
			}
			read();
		})
		.catch(err => {
			window.goStreamDone(err.message);
		});
	}`, string(reqJSON))

	if _, err := page.Evaluate(rod.Eval(script)); err != nil {
		return fmt.Errorf("执行失败: %w", err)
	}

	// 等待流结束
	select {
	case err := <-done:
		return err
	case <-time.After(90 * time.Second):
		return fmt.Errorf("请求超时")
	}
}
