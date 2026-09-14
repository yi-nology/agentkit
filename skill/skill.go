// Package skill 内容/方法论解析器。
// 从目录加载命名内容文件（SKILL.md、prompt 模板、方法论文档），
// 带路径遍历保护（`..` 段拒绝；注意 symlink 不在防护范围——root 应为可信目录）
// + 进程内缓存（无淘汰，skill 内容假定进程生命周期内不变）+ checksum。
// 文件解析部分零外部依赖；决策使用（decision.go）依赖 eino。
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
	Version     string
	Description string // frontmatter description（无则空）
	Content     string // frontmatter 之后的正文
	Checksum    string // sha256(content) 前 16 位
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

// Resolve 解析引用，返回内容（带缓存）。
func (p *FileProvider) Resolve(_ context.Context, ref Ref) (*Skill, error) {
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
	// frontmatter 元数据剥离（正文不含围栏；checksum 对全文计算防漂移）
	fmName, fmDesc, body := parseFrontmatter(string(data))
	sum := sha256.Sum256(data)
	displayName := ref.Name
	if fmName != "" {
		displayName = fmName
	}
	s := &Skill{
		Name:        displayName,
		Version:     ref.Version,
		Description: fmDesc,
		Content:     body,
		Checksum:    hex.EncodeToString(sum[:8]),
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
// 未命中返回 ("", false)。别名表懒扫描构建（进程内 skill 目录通常不变）。
func (p *FileProvider) CanonicalName(name string) (string, bool) {
	p.mu.RLock()
	aliases := p.aliases
	p.mu.RUnlock()
	if aliases == nil {
		aliases = p.scanAliases()
	}
	n, ok := aliases[name]
	return n, ok
}

func (p *FileProvider) scanAliases() map[string]string {
	metas, err := p.ListSkills(context.Background())
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
	CanonicalName(name string) (string, bool)
}

// 编译期断言：FileProvider 支持发现与别名归一化。
var (
	_ Lister        = (*FileProvider)(nil)
	_ AliasResolver = (*FileProvider)(nil)
)
