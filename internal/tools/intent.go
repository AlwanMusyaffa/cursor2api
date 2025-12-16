package tools

import (
	"regexp"
	"strings"
)

// IntentParser 解析用户意图
type IntentParser struct{}

// NewIntentParser 创建意图解析器
func NewIntentParser() *IntentParser {
	return &IntentParser{}
}

// Intent 用户意图
type Intent struct {
	Action   string // create_file, read_file, run_command, edit_file, list_dir
	FilePath string
	Content  string
	Command  string
}

// ParseUserIntent 从用户消息解析意图
func (p *IntentParser) ParseUserIntent(messages []string) *Intent {
	// 合并所有用户消息
	text := strings.Join(messages, " ")
	text = strings.ToLower(text)

	intent := &Intent{}

	// 检测创建文件意图
	createPatterns := []string{
		`创建.*?文件`,
		`create.*?file`,
		`写入.*?文件`,
		`write.*?to`,
		`帮我创建`,
		`新建`,
	}
	for _, pattern := range createPatterns {
		if matched, _ := regexp.MatchString(pattern, text); matched {
			intent.Action = "create_file"
			break
		}
	}

	// 检测读取文件意图
	readPatterns := []string{
		`读取.*?文件`,
		`read.*?file`,
		`查看.*?文件`,
		`看.*?内容`,
		`cat\s+`,
	}
	for _, pattern := range readPatterns {
		if matched, _ := regexp.MatchString(pattern, text); matched {
			intent.Action = "read_file"
			break
		}
	}

	// 检测执行命令意图
	cmdPatterns := []string{
		`执行.*?命令`,
		`run.*?command`,
		`运行`,
		`execute`,
	}
	for _, pattern := range cmdPatterns {
		if matched, _ := regexp.MatchString(pattern, text); matched {
			intent.Action = "run_command"
			break
		}
	}

	// 提取文件路径
	pathPatterns := []*regexp.Regexp{
		regexp.MustCompile(`['"](\/[^'"]+)['""]`),
		regexp.MustCompile(`['""]([^'"]+\.\w+)['""]`),
		regexp.MustCompile(`(\S+\.\w{1,5})\b`),
	}
	for _, re := range pathPatterns {
		if matches := re.FindStringSubmatch(strings.Join(messages, " ")); len(matches) > 1 {
			intent.FilePath = matches[1]
			break
		}
	}

	// 提取内容（在"内容"、"content"后面的文本）
	contentPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)内容[是为:：\s]+['""]?(.+?)['""]?\s*$`),
		regexp.MustCompile(`(?i)content[:\s]+['""]?(.+?)['""]?\s*$`),
		regexp.MustCompile(`['""]([^'"]+)['""]`),
	}
	for _, re := range contentPatterns {
		if matches := re.FindStringSubmatch(strings.Join(messages, " ")); len(matches) > 1 {
			intent.Content = matches[1]
			break
		}
	}

	return intent
}

// DetectRefusal 检测 AI 是否拒绝执行
func DetectRefusal(response string) bool {
	refusalPatterns := []string{
		"无法直接",
		"无法执行",
		"不能执行",
		"受到了限制",
		"没有权限",
		"无法帮你",
		"cannot directly",
		"unable to",
		"don't have access",
		"I can't",
		"我不能",
		"我无法",
		"请在你的终端",
		"请在本地",
		"你需要在",
		"你可以运行",
	}

	responseLower := strings.ToLower(response)
	for _, pattern := range refusalPatterns {
		if strings.Contains(responseLower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// ExtractCommandFromRefusal 从拒绝响应中提取建议的命令
func ExtractCommandFromRefusal(response string) string {
	// 匹配代码块中的命令
	codeBlockRe := regexp.MustCompile("```(?:bash|sh)?\\s*\\n?([^`]+)\\n?```")
	if matches := codeBlockRe.FindStringSubmatch(response); len(matches) > 1 {
		cmd := strings.TrimSpace(matches[1])
		if cmd != "" {
			return cmd
		}
	}

	// 匹配常见命令模式（每行检查）
	lines := strings.Split(response, "\n")
	cmdPatterns := []*regexp.Regexp{
		// echo "xxx" > file
		regexp.MustCompile(`^\s*(echo\s+.+\s*>\s*\S+)`),
		// cat > file << 'EOF' 或 cat > file
		regexp.MustCompile(`^\s*(cat\s+.+\s*>\s*\S+)`),
		// 常见命令开头
		regexp.MustCompile(`^\s*((?:echo|cat|mkdir|touch|rm|cp|mv|ls|pwd|cd|chmod|chown)\s+.+)$`),
		// 任何 > 重定向
		regexp.MustCompile(`^\s*(\S+\s+["'][^"']+["']\s*>\s*\S+)`),
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, re := range cmdPatterns {
			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
				return strings.TrimSpace(matches[1])
			}
		}
	}

	return ""
}
