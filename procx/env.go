package procx

import (
	"os"
	"strings"
)

// baseEnvAllow 子进程缺省环境白名单：路径/临时目录/语言/代理/CA。
// 绝不全量继承——cli agent / MCP server 等待启动的子进程会在用户工作目录执行
// 任意代码，全量环境等于把宿主凭证交给待执行任务。
// 本表是全仓库子进程环境纪律的单一事实源（procx.Run / acpx 适配器 / mcp
// stdio server 共用）；新增基础变量只改这里。
var baseEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "USER": true,
	"LOGNAME": true, "SHELL": true,
	"LANG": true, "LC_ALL": true, "TERM": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	// agent 自身的配置目录（登录态）：按名显式放行
	"CLAUDE_CONFIG_DIR": true, "OPENCODE_CONFIG": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
}

// ChildEnv 构造子进程最小环境（导出：mcp 等自行 spawn 外部子进程的包共用同一
// 纪律；经 procx.Run 执行时无需调用——Run 内部已应用）：
// 白名单 + 显式透传项 + 禁交互。
// extra 两种形态：纯名（如 "HF_TOKEN"）从当前进程按名透传（不存在则丢弃）；
// 含 "="（如 "HF_TOKEN=xxx"）按 KEY=VALUE 字面注入，且同名父进程值不透传
// （每 key 唯一，避免 environ 同名两项时子进程取值依实现而异）。
// 尾部固定追加 GIT_TERMINAL_PROMPT=0（git 弹终端提问会挂到超时）与 CI=1——
// 对非 git 子进程无害。
func ChildEnv(extra []string) []string {
	keep := map[string]bool{}
	literal := map[string]string{} // name → KEY=VALUE
	var litOrder []string
	for _, k := range extra {
		if i := strings.IndexByte(k, '='); i > 0 {
			name := k[:i]
			if _, dup := literal[name]; !dup {
				litOrder = append(litOrder, name)
			}
			literal[name] = k
		} else {
			keep[k] = true
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, isLit := literal[name]; isLit {
			continue // 字面注入项优先，不透传父进程同名值
		}
		if baseEnvAllow[name] || keep[name] {
			env = append(env, kv)
		}
	}
	for _, name := range litOrder {
		env = append(env, literal[name])
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "CI=1")
}
