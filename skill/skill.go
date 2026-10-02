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
type Skill struct {
	Name     string
	Version  string
	Content  string
	Checksum string // sha256(content) 前 16 位
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
	sum := sha256.Sum256(data)
	s := &Skill{
		Name:     ref.Name,
		Version:  ref.Version,
		Content:  string(data),
		Checksum: hex.EncodeToString(sum[:8]),
	}
	p.mu.Lock()
	p.cache[key] = s
	p.mu.Unlock()
	return s, nil
}
