package acpx

import (
	"os"
	"strings"
)

// tempFile 创建临时文件，返回路径与清理函数。
func tempFile(pattern string) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	_ = f.Close()
	return path, func() { _ = os.Remove(path) }, nil
}

// readFileTrim 读取文件并去除首尾空白（不存在返回空串）。
func readFileTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// mapSandbox 沙箱档位 → CLI 参数值（readonly/workspace/full 三档语义统一，
// 取值因 CLI 而异由各适配器传入）。未指定档位 ok=false（不追加任何参数）。
func mapSandbox(sb, readonly, workspace, full string) (string, bool) {
	switch sb {
	case SandboxReadonly:
		return readonly, true
	case SandboxWorkspace:
		return workspace, true
	case SandboxFull:
		return full, true
	}
	return "", false
}

// namedAgent 注册身份单源（ClaudeCode/Gemini/Mimo/Pi 四家的 Name/bin 兜底
// 逐字重复收敛，收口轮）：name 非空 = 注册身份（构造器填充）；空 = 缺省名。
// 缺省名同时是 Bin 为空时的可执行名兜底（exec 同源）。
type namedAgent struct {
	name     string // 注册身份（构造器填充）
	fallback string // 缺省名（零值构造/Bin 空时的执行名兜底）
}

// Name Agent 接口的 Name 实现（适配器嵌入即得）。
func (a *namedAgent) Name() string {
	if a.name != "" {
		return a.name
	}
	return a.fallback
}

// exec 可执行名（Bin 非空用 Bin，空回退缺省名——零值适配器不致 exec ""）。
func (a *namedAgent) exec(bin string) string {
	if bin != "" {
		return bin
	}
	return a.fallback
}
