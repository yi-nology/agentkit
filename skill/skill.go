// Package skill 内容/方法论解析器。
// 从目录加载命名内容文件（SKILL.md、prompt 模板、方法论文档），
// 带路径遍历保护 + 缓存 + checksum。零外部依赖。
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
	Name        string
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

	mu    sync.RWMutex
	cache map[string]*Skill // key = name@version
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

	name := filepath.Clean("/" + strings.TrimSpace(ref.Name))[1:] // 防路径穿越
	if name == "" || strings.Contains(name, "..") {
		return nil, fmt.Errorf("skill: 非法引用 %q", ref.Name)
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
func (p *FileProvider) ListSkills(_ context.Context) ([]Meta, error) {
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
		s, err := p.Resolve(context.Background(), Ref{Name: refName})
		if err != nil {
			continue // 单个 skill 损坏不阻塞发现
		}
		out = append(out, Meta{Name: s.Name, Version: s.Version, Description: s.Description})
	}
	return out, nil
}

// Lister 可选能力：支持"发现 + 决策使用"的 Provider（渐进披露模式需要）。
type Lister interface {
	ListSkills(ctx context.Context) ([]Meta, error)
}

// 编译期断言：FileProvider 支持发现。
var _ Lister = (*FileProvider)(nil)