// Package workcopy Git 工作副本沙箱服务。
// 浅克隆 base 默认分支 + fetch PR head + checkout，供 cli 插件在真实工作树上执行。
// 凭证嵌在 clone URL 中，绝不落盘。singleflight 防并发重复 clone。
// 引用归零后目录**保留**（供同 PR 下次审查增量复用，省 base 分支重克隆），
// TTL 由 Sweep 兜底回收。
package workcopy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"git.enjoye.top/enjoydream/ekit/observability/logx"
	"golang.org/x/sync/singleflight"
)

// WorktreeKey 工作副本标识（平台/仓库/PR/SHA）。
type WorktreeKey struct {
	Platform      string
	Owner         string
	Repo          string
	Number        string
	HeadSHA       string
	DefaultBranch string
}

func (k WorktreeKey) String() string {
	return fmt.Sprintf("%s/%s/%s#%s@%s", k.Platform, k.Owner, k.Repo, k.Number, k.HeadSHA)
}

// Pool 工作副本沙箱池。
type Pool struct {
	Root string
	Log  logx.Logger

	// CredentialOf 返回 (cloneBaseURL, token)：由调用方按平台注入。
	CredentialOf func(platform string) (baseURL, token string, ok bool)
	// InsecureTLSOf 平台是否免 TLS 校验。
	InsecureTLSOf func(platform string) bool

	mu      sync.Mutex
	entries map[string]*wcEntry
	group   singleflight.Group
}

type wcEntry struct {
	key      WorktreeKey // 当前 checkout 的 key（同 PR 换 head 后随 refresh 前移）
	dir      string
	refCount int
	lastUsed time.Time
}

// NewPool 创建工作副本沙箱池。
func NewPool(root string, log logx.Logger) *Pool {
	return &Pool{Root: root, Log: log, entries: map[string]*wcEntry{}}
}

// Ensure 返回就绪的工作副本目录（浅克隆 base + checkout PR head）。
// 同 key 并发调用共享同一目录（引用计数）。
func (p *Pool) Ensure(ctx context.Context, key WorktreeKey) (string, error) {
	if p.CredentialOf == nil {
		return "", fmt.Errorf("workcopy: 未配置 CredentialOf")
	}
	baseURL, token, ok := p.CredentialOf(key.Platform)
	if !ok || token == "" {
		return "", fmt.Errorf("workcopy: 平台 %s 缺少 clone 凭证", key.Platform)
	}
	if key.HeadSHA == "" {
		return "", fmt.Errorf("workcopy: PR %s/%s#%s 无 head SHA", key.Owner, key.Repo, key.Number)
	}

	k := key.String()
	p.mu.Lock()
	if e, ok := p.entries[k]; ok {
		e.refCount++
		e.lastUsed = time.Now()
		dir := e.dir
		p.mu.Unlock()
		return dir, nil
	}
	// 同 PR 换 head（新推送）：领用引用归零的保留目录做增量刷新——只 fetch 新的
	// PR refspec，base 分支浅对象已在库，省掉整次浅克隆。领用（rc++）阻断 Sweep
	// 与并发二次领用；刷新失败回落全新克隆。
	var claim *wcEntry
	for _, e := range p.entries {
		if e.refCount <= 0 &&
			e.key.Platform == key.Platform && e.key.Owner == key.Owner &&
			e.key.Repo == key.Repo && e.key.Number == key.Number &&
			e.key.HeadSHA != key.HeadSHA {
			e.refCount++
			claim = e
			break
		}
	}
	p.mu.Unlock()

	if claim != nil {
		if err := p.refresh(ctx, claim.dir, key, token); err == nil {
			p.mu.Lock()
			if e, ok := p.entries[k]; ok {
				// 竞争窗口内同 key 已被常规建仓登记：既有条目胜出。claim 条目仍挂在
				// 旧 key 下且工作树已被推进到新 head——留着会让旧 key 命中错误内容，
				// 必须整条作废
				e.refCount++
				e.lastUsed = time.Now()
				dir := e.dir
				delete(p.entries, claim.key.String())
				p.mu.Unlock()
				_ = os.RemoveAll(claim.dir)
				return dir, nil
			}
			delete(p.entries, claim.key.String())
			claim.key = key
			claim.lastUsed = time.Now()
			p.entries[k] = claim
			dir := claim.dir
			p.mu.Unlock()
			return dir, nil
		}
		// 刷新失败（force push 抹掉旧引用等）→ 释放领用，回落全新克隆
		p.mu.Lock()
		claim.refCount--
		p.mu.Unlock()
	}

	// 建仓与登记同在 singleflight 内完成（条目 rc=0，引用由 Do 返回后统一加）：
	// 共享者退出时若条目已消失（同 flight 先到者的条目被 Sweep 回收或 refresh 重挂
	// key），重走一次建仓，杜绝拿到已删除目录
	for round := 0; ; round++ {
		// 返回值不消费：建仓与登记同在 flight 内，Do 返回后统一从 entries 取
		_, err, _ := p.group.Do(k, func() (any, error) {
			dir, err := p.prepare(ctx, key, baseURL, token)
			if err != nil {
				return nil, err
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if e, ok := p.entries[k]; ok { // 双检：并发建仓时保留先到者
				_ = os.RemoveAll(dir)
				return e.dir, nil
			}
			p.entries[k] = &wcEntry{key: key, dir: dir, lastUsed: time.Now()}
			return dir, nil
		})
		if err != nil {
			return "", err
		}

		p.mu.Lock()
		e, ok := p.entries[k]
		if ok {
			e.refCount++
			e.lastUsed = time.Now()
			dir := e.dir
			p.mu.Unlock()
			return dir, nil
		}
		p.mu.Unlock()
		if round >= 1 {
			return "", fmt.Errorf("workcopy: %s 工作副本登记后丢失（并发释放竞争）", k)
		}
	}
}

// Release 任务结束后释放。引用归零**不再立即删目录**：保留供同 PR 下次审查
// 增量复用（换 head 只 fetch 新 refspec），TTL 由 Sweep 兜底回收。
func (p *Pool) Release(key WorktreeKey) {
	k := key.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[k]
	if !ok {
		return
	}
	e.refCount--
	if e.refCount <= 0 {
		e.lastUsed = time.Now()
	}
}

func prRefspec(platform, number string) string {
	switch strings.ToLower(platform) {
	case "gitlab":
		return fmt.Sprintf("refs/merge-requests/%s/head", number)
	default:
		return fmt.Sprintf("refs/pull/%s/head", number)
	}
}

func cloneURL(baseURL, owner, repo, token string) string {
	u := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if strings.HasPrefix(u, "file://") {
		return u
	}
	for _, suffix := range []string{"/api/v1", "/api/v4", "/api/v3", "/api"} {
		u = strings.TrimSuffix(u, suffix)
	}
	// 保留原有 scheme——内网 http://gitea 被强制升 https 会导致克隆失败
	scheme := "https://"
	if i := strings.Index(u, "://"); i >= 0 {
		scheme = u[:i+3]
		u = u[i+3:]
	}
	return fmt.Sprintf("%soauth2:%s@%s/%s/%s.git", scheme, token, u, owner, repo)
}

func (p *Pool) prepare(ctx context.Context, key WorktreeKey, baseURL, token string) (string, error) {
	// branch 来自外部输入（DefaultBranch）：以 "-" 开头会被 git 当选项解析
	// （`--branch --upload-pack=…` 即 argument injection），含空白/控制字符则
	// 本身不是合法引用名——在建任何目录之前 fail-fast
	if b := key.DefaultBranch; strings.HasPrefix(b, "-") || strings.ContainsAny(b, " \t\r\n\x7f") {
		return "", fmt.Errorf("workcopy: 非法引用名 %q", b)
	}
	if err := os.MkdirAll(p.Root, 0o755); err != nil {
		return "", fmt.Errorf("workcopy: 根目录创建失败: %w", err)
	}
	dir, err := os.MkdirTemp(p.Root, "wc-")
	if err != nil {
		return "", fmt.Errorf("workcopy: 建沙箱失败: %w", err)
	}
	branch := key.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	url := cloneURL(baseURL, key.Owner, key.Repo, token)
	insecureTLS := p.InsecureTLSOf != nil && p.InsecureTLSOf(key.Platform)

	steps := [][]string{
		// clone 支持 `--` 终止选项解析：url/dir 此后恒为位置参数
		{"git", "clone", "--depth", "1", "--branch", branch, "--", url, dir},
		{"git", "-C", dir, "fetch", "--depth", "1", "origin", prRefspec(key.Platform, key.Number)},
		{"git", "-C", dir, "checkout", "--force", "FETCH_HEAD"},
	}
	for _, args := range steps {
		if err := runGit(ctx, args, insecureTLS); err != nil {
			_ = os.RemoveAll(dir)
			// msg 为脱敏载体，token 必须作为 secret 传入（此前参数顺序传反，
			// git 失败详情整体丢失、脱敏形同虚设）
			return "", fmt.Errorf("workcopy: %s#%s 准备失败: %s",
				key.Owner, key.Repo, scrub(err.Error(), token))
		}
	}
	p.Log.Info("agentkit.workcopy.ready", "dir", dir, "pr", key.Owner+"/"+key.Repo+"#"+key.Number)
	return dir, nil
}

// refresh 将保留目录增量推进到新 PR head：只 fetch 新的 PR refspec（base 分支
// 浅对象已在库——这是"第二次审查少拉取"的收益来源），checkout --force 与
// prepare 末步同款。origin/<默认分支> 不随刷新前移，base 端新鲜度以首次克隆
// 为界（由保留 TTL 限定窗口）；默认分支大跨度强推场景由调用方全新克隆兜底
//（refresh 失败即回落 prepare）。
func (p *Pool) refresh(ctx context.Context, dir string, key WorktreeKey, token string) error {
	insecureTLS := p.InsecureTLSOf != nil && p.InsecureTLSOf(key.Platform)
	steps := [][]string{
		{"git", "-C", dir, "fetch", "--depth", "1", "origin", prRefspec(key.Platform, key.Number)},
		{"git", "-C", dir, "checkout", "--force", "FETCH_HEAD"},
	}
	for _, args := range steps {
		if err := runGit(ctx, args, insecureTLS); err != nil {
			return fmt.Errorf("workcopy: %s#%s 增量刷新失败: %s",
				key.Owner, key.Repo, scrub(err.Error(), token))
		}
	}
	p.Log.Info("agentkit.workcopy.refreshed", "dir", filepath.Base(dir), "pr", key.Owner+"/"+key.Repo+"#"+key.Number)
	return nil
}

func scrub(msg string, secrets ...string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, "oauth2:"+s+"@", "***@")
		msg = strings.ReplaceAll(msg, s, "***")
	}
	return msg
}

func runGit(ctx context.Context, args []string, insecureTLS bool) error {
	c, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(c, args[0], args[1:]...)
	// 禁交互：认证失败时 git 弹终端提问会挂到超时
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if insecureTLS {
		cmd.Env = append(cmd.Env, "GIT_SSL_NO_VERIFY=true")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(tail))
	}
	return nil
}

// Sweep 清理泄漏的沙箱目录（兜底 TTL 扫描）。
// 只回收引用归零且超 TTL 的条目与孤儿目录——rc>0 的在用目录一律保留
// （长任务超 TTL 时被删即是生产事故），因此 TTL 应配置为大于最长任务时长；
// 进程崩溃导致的泄漏目录（条目随进程消失）由下方孤儿扫描兜底。
func (p *Pool) Sweep(ttl time.Duration) {
	p.mu.Lock()
	var stale []string
	live := make(map[string]bool, len(p.entries))
	for k, e := range p.entries {
		live[e.dir] = true
		if e.refCount <= 0 && time.Since(e.lastUsed) > ttl {
			stale = append(stale, e.dir)
			delete(p.entries, k)
		}
	}
	p.mu.Unlock()
	for _, d := range stale {
		_ = os.RemoveAll(d)
		p.Log.Warn("agentkit.workcopy.swept", "dir", filepath.Base(d))
	}
	orphans, _ := filepath.Glob(filepath.Join(p.Root, "wc-*"))
	for _, d := range orphans {
		if live[d] {
			continue
		}
		info, err := os.Stat(d)
		if err != nil || time.Since(info.ModTime()) <= ttl {
			continue
		}
		_ = os.RemoveAll(d)
		p.Log.Warn("agentkit.workcopy.orphan_swept", "dir", filepath.Base(d))
	}
}
