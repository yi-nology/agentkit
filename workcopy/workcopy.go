// Package workcopy Git 工作副本沙箱服务。
// 浅克隆 base 默认分支 + fetch PR head + checkout，供 cli 插件在真实工作树上执行。
// 凭证嵌在 clone URL 中，绝不落盘。singleflight 防并发重复 clone。
// 引用计数 + TTL 扫描回收泄漏目录。
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
	p.mu.Unlock()

	result, err, _ := p.group.Do(k, func() (any, error) {
		return p.prepare(ctx, key, baseURL, token)
	})
	if err != nil {
		return "", err
	}
	dir := result.(string)

	p.mu.Lock()
	if e, ok := p.entries[k]; ok {
		e.refCount++
		p.mu.Unlock()
		_ = os.RemoveAll(dir)
		return e.dir, nil
	}
	p.entries[k] = &wcEntry{dir: dir, refCount: 1, lastUsed: time.Now()}
	p.mu.Unlock()
	return dir, nil
}

// Release 任务结束后释放（引用归零即删沙箱目录）。
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
		_ = os.RemoveAll(e.dir)
		delete(p.entries, k)
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
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	return fmt.Sprintf("https://oauth2:%s@%s/%s/%s.git", token, stripScheme(u), owner, repo)
}

func stripScheme(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		return u[i+3:]
	}
	return u
}

func (p *Pool) prepare(ctx context.Context, key WorktreeKey, baseURL, token string) (string, error) {
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
		{"git", "clone", "--depth", "1", "--branch", branch, url, dir},
		{"git", "-C", dir, "fetch", "--depth", "1", "origin", prRefspec(key.Platform, key.Number)},
		{"git", "-C", dir, "checkout", "--force", "FETCH_HEAD"},
	}
	for _, args := range steps {
		if err := runGit(ctx, args, insecureTLS); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("workcopy: %s#%s 准备失败: %s",
				key.Owner, key.Repo, scrub(key.Number, err.Error(), token))
		}
	}
	p.Log.Info("agentkit.workcopy.ready", "dir", dir, "pr", key.Owner+"/"+key.Repo+"#"+key.Number)
	return dir, nil
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
	if insecureTLS {
		cmd.Env = append(os.Environ(), "GIT_SSL_NO_VERIFY=true")
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

// Sweep 清理引用计数泄漏的沙箱目录（兜底 TTL 扫描）。
func (p *Pool) Sweep(ttl time.Duration) {
	p.mu.Lock()
	var stale []string
	live := make(map[string]bool, len(p.entries))
	for k, e := range p.entries {
		live[e.dir] = true
		if e.refCount <= 0 || time.Since(e.lastUsed) > ttl {
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
