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
