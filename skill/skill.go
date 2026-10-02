// Package skill 技能内容与元数据：SKILL.md 解析（frontmatter 围栏 + canonical
// checksum）、进程内技能库（多根扫描、热替换）、版本区间约束（decl/version）、
// 决策式按需加载（decision.go 的 use_skill 渐进披露）。
// 内容文件解析带路径遍历保护（`..` 段拒绝；注意 symlink 不在防护范围——root
// 应为可信目录）；文件解析部分零外部依赖，决策使用依赖 eino。
package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Ref 内容引用。
type Ref struct {
	Name    string
	Version string
	Source  string // file（v3.0 仅实现 file）
}

// Skill 解析后的内容（带 checksum 防内容漂移难排查）。
// SKILL.md 支持 Agent Skills 标准的 frontmatter（--- 围栏内 name/description 行）：
// description 服务于发现与决策使用（渐进披露），正文才是消费主体。
type Skill struct {
	Name        string
	Version     string // frontmatter 声明（未声明为空串）；ref.Version 是请求约束/缓存键，不回显
	Description string // frontmatter description（无则空）
	Content     string // frontmatter 之后的正文
	// Checksum 内容校验和，canonical 口径：sha256(正文) 前 16 位 hex——绑定
	// 实际注入提示词的内容（frontmatter 是元数据，不进 prompt）。
	// FileProvider 与 Library 两个 Provider 一致，观测守卫的比对基准不随 Provider 漂移。
	Checksum string
}

// contentChecksum canonical 校验和（sha256 hex 前 16 位），两个 Provider 共用单源。
func contentChecksum(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:16]
}

// Meta skill 元数据（不含正文——渐进披露：提示词只注入这一层）。
type Meta struct {
	// Name 规范引用名（目录名/文件名）——use_skill 加载与 allowed 清单的唯一依据。
	Name string
	// Title 展示名（frontmatter name；与 Name 相同或目录缺失时为空）。
	// 仅用于清单展示，加载仍按 Name；模型若用 Title 调用，AsSkillTool 会归一化。
	Title       string
	Version     string
	Description string
}

// Provider 内容解析接口。
type Provider interface {
	Resolve(ctx context.Context, ref Ref) (*Skill, error)
}

// FileProvider file 来源实现：root/<name>/SKILL.md 或 root/<name>.md。
type FileProvider struct {
	Root string

	mu      sync.RWMutex
	cache   map[string]*Skill // key = name@version
	aliases map[string]string // 展示名(frontmatter name) → 规范引用名（懒扫描）
}

// NewFileProvider 创建文件来源的内容解析器。
func NewFileProvider(root string) *FileProvider {
	return &FileProvider{Root: root, cache: map[string]*Skill{}}
}

func cacheKey(ref Ref) string {
	if ref.Version != "" {
		return ref.Name + "@" + ref.Version
	}
	return ref.Name
}

// Resolve 解析引用，返回内容（带缓存）。磁盘读取前检查 ctx 取消。
func (p *FileProvider) Resolve(ctx context.Context, ref Ref) (*Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("skill: %w", err)
	}
	if ref.Source == "" {
		ref.Source = "file"
	}
	if ref.Source != "file" {
		return nil, fmt.Errorf("skill: 来源 %q 暂未实现（仅支持 file），ref=%s", ref.Source, ref.Name)
	}

	key := cacheKey(ref)
	p.mu.RLock()
	cached := p.cache[key]
	p.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	name := filepath.Clean("/" + strings.TrimSpace(ref.Name))[1:] // 防路径穿越（前缀 / 使 .. 无法越出 root）
	if name == "" {
		return nil, fmt.Errorf("skill: 非法引用 %q", ref.Name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." { // 仅拒绝路径段级的 ..，放行 foo..bar 这类合法名
			return nil, fmt.Errorf("skill: 非法引用 %q", ref.Name)
		}
	}
	candidates := []string{
		filepath.Join(p.Root, name, "SKILL.md"),
		filepath.Join(p.Root, name+".md"),
	}
	var data []byte
	for _, c := range candidates {
		if b, err := os.ReadFile(c); err == nil {
			data = b
			break
		}
	}
	if data == nil {
		return nil, fmt.Errorf("skill: 未找到 %s（尝试 %v）", ref.Name, candidates)
	}
	// frontmatter 元数据剥离；checksum 对正文计算（canonical 口径，见 Skill.Checksum）
	fmName, fmDesc, fmVer, body := parseFrontmatter(string(data))
	displayName := ref.Name
	if fmName != "" {
		displayName = fmName
	}
	s := &Skill{
		Name:        displayName,
		Version:     fmVer,
		Description: fmDesc,
		Content:     body,
		Checksum:    contentChecksum(body),
	}
	p.mu.Lock()
	p.cache[key] = s
	p.mu.Unlock()
	return s, nil
}

// ---------- 发现与决策使用（渐进披露） ----------

// ListSkills 枚举目录下全部 skill 的元数据（不含正文）。
// 供提示词注入"可用 skill 清单"——agent 据此决策加载哪个（经 use_skill 工具）。
// 实现 Lister 接口。
func (p *FileProvider) ListSkills(ctx context.Context) ([]Meta, error) {
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return nil, fmt.Errorf("skill: 目录读取失败 %s: %w", p.Root, err)
	}
	var out []Meta
	for _, e := range entries {
		name := e.Name()
		var refName string
		switch {
		case e.IsDir():
			refName = name // root/<name>/SKILL.md
		case strings.HasSuffix(name, ".md"):
			refName = strings.TrimSuffix(name, ".md") // root/<name>.md
		default:
			continue
		}
		s, err := p.Resolve(ctx, Ref{Name: refName})
		if err != nil {
			continue // 单个 skill 损坏不阻塞发现
		}
		title := ""
		if s.Name != refName {
			title = s.Name // frontmatter name 与目录名不同：作为展示别名
		}
		out = append(out, Meta{Name: refName, Title: title, Version: s.Version, Description: s.Description})
	}
	return out, nil
}

// CanonicalName 把展示名（frontmatter name）归一化为规范引用名（目录名/文件名）。
// 未命中返回 ("", false)。别名表懒扫描构建（进程内 skill 目录通常不变）；
// 首次扫描为全目录磁盘 I/O，ctx 取消随链透传。
func (p *FileProvider) CanonicalName(ctx context.Context, name string) (string, bool) {
	p.mu.RLock()
	aliases := p.aliases
	p.mu.RUnlock()
	if aliases == nil {
		aliases = p.scanAliases(ctx)
	}
	n, ok := aliases[name]
	return n, ok
}

func (p *FileProvider) scanAliases(ctx context.Context) map[string]string {
	metas, err := p.ListSkills(ctx)
	m := map[string]string{}
	if err == nil {
		for _, meta := range metas {
			if meta.Title != "" && meta.Title != meta.Name {
				m[meta.Title] = meta.Name
			}
		}
	}
	p.mu.Lock()
	p.aliases = m
	p.mu.Unlock()
	return m
}

// Lister 可选能力：支持"发现 + 决策使用"的 Provider（渐进披露模式需要）。
type Lister interface {
	ListSkills(ctx context.Context) ([]Meta, error)
}

// AliasResolver 可选能力：把展示别名归一化为规范引用名（渐进披露链路闭环：
// 清单可能展示 frontmatter name，模型会原样回填给 use_skill）。
type AliasResolver interface {
	CanonicalName(ctx context.Context, name string) (string, bool)
}

// 编译期断言：FileProvider 支持发现与别名归一化。
var (
	_ Lister        = (*FileProvider)(nil)
	_ AliasResolver = (*FileProvider)(nil)
)
