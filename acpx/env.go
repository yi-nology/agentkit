package acpx

import (
	"os"
	"strings"
)

// baseEnvAllow 子进程缺省环境白名单：路径/临时目录/语言/代理/CA。
// 绝不全量继承——cli agent 在用户工作目录执行任意代码，
// 全量环境等于把宿主凭证交给待执行任务。
var baseEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "USER": true,
	"LANG": true, "LC_ALL": true, "TERM": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	// agent 自身的配置目录（登录态）：按名显式放行
	"CLAUDE_CONFIG_DIR": true, "OPENCODE_CONFIG": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
}

// childEnv 构造子进程最小环境：白名单 + 显式透传项 + 禁交互。
// extra 两种形态：纯名（如 "HF_TOKEN"）从当前进程按名透传（不存在则丢弃）；
// 含 "="（如 "HF_TOKEN=xxx"）按 KEY=VALUE 字面注入，且同名父进程值不透传
// （每 key 唯一，避免 environ 同名两项时子进程取值依实现而异）。
func childEnv(extra []string) []string {
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
