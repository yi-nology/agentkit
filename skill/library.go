package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 技能生命周期常量（通用概念，非特定平台专属）。
const (
	ModeStatic     = "static"    // 全文注入系统提示
	ModeOnDemand   = "on_demand" // 决策式：清单 + use_skill 按需加载
	DefaultVersion = "0.0.0"

	MaturityExperimental = "experimental"
	MaturityStable       = "stable"
	MaturityFrozen       = "frozen"
	MaturityDeprecated   = "deprecated"
)

// MCPDep 技能对 MCP server 的依赖声明（静态契约：血缘展示与加载期对账用，
// 不做运行时派发拦截）。Tools 为本技能实际用到的工具名（血缘展示 + 清单对账）；
// MinVersion 为对 server 契约清单 version 的下限声明（SemVer，警告级对账）。
type MCPDep struct {
	Server     string   `yaml:"server" json:"server"`
	Tools      []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	MinVersion string   `yaml:"min_version,omitempty" json:"min_version,omitempty"`
	Comment    string   `yaml:"comment,omitempty" json:"comment,omitempty"`
}

// Deprecated 弃用窗口（maturity=deprecated 时 RemoveAfter 必填，YYYY-MM-DD）。
type Deprecated struct {
	Reason      string `yaml:"reason,omitempty" json:"reason,omitempty"`
	ReplacedBy  string `yaml:"replaced_by,omitempty" json:"replaced_by,omitempty"` // 替代技能名（可空）
	RemoveAfter string `yaml:"remove_after,omitempty" json:"remove_after,omitempty"`
}

// LibMeta 技能完整元数据（渐进披露清单 + 生命周期管理）。
// JSON 标签即对外 API 契约（名册/血缘面），变更须同步消费方。
type LibMeta struct {
	Name        string   `yaml:"name" json:"name"`
	Title       string   `yaml:"-" json:"-"` // frontmatter name（展示名）；目录名规范
	Description string   `yaml:"description" json:"description"`
	Mode        string   `yaml:"mode" json:"mode"`
	Version     string   `yaml:"version,omitempty" json:"version,omitempty"`
	Maturity    string   `yaml:"maturity,omitempty" json:"maturity,omitempty"`
	RequiresMCP []MCPDep `yaml:"requires_mcp,omitempty" json:"requires_mcp,omitempty"`
	// RequiresConfig 技能依赖的集成配置类型（bianque 集成平面）：如 [rag, s3]。
	// 声明级契约——缺失由调用方告警，不拦截加载。
	RequiresConfig []string    `yaml:"requires_config,omitempty" json:"requires_config,omitempty"`
	Deprecated     *Deprecated `yaml:"deprecated,omitempty" json:"deprecated,omitempty"`
	Provides       []string    `yaml:"provides,omitempty" json:"provides,omitempty"` // 能力标签（登记/展示；重复声明由调用方告警）
	Compatibility  string      `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Source         string      `yaml:"-" json:"source"` // 来源前缀标签
	Path           string      `yaml:"-" json:"-"`      // SKILL.md 相对路径
}

// DeprecationExpired 弃用窗口是否已过（remove_after 当日结束算未过期）。
func (m LibMeta) DeprecationExpired(now time.Time) bool {
	if m.Maturity != MaturityDeprecated || m.Deprecated == nil || m.Deprecated.RemoveAfter == "" {
		return false
	}
	d, err := time.ParseInLocation("2006-01-02", m.Deprecated.RemoveAfter, time.Local)
	if err != nil {
		return false
	}
	return now.After(d.Add(24*time.Hour - time.Nanosecond))
}

// LibEntry 库内单技能。
type LibEntry struct {
	Full string // 全文（含 frontmatter）
	Body string // 剥 frontmatter 正文
	Meta LibMeta
}

// Library 进程内技能库（可热替换；无缓存，每次读当前实例）。
// 实现 Provider / Lister / AliasResolver。
type Library struct {
	byName map[string]LibEntry
}

// LoadOption LoadFromFS 选项。
type LoadOption func(*loadConfig)

type loadConfig struct {
	// prefixes 额外扫描前缀（相对 fsys 根）；每个前缀下一层子目录名=技能名。
	// 缺省：扫描根下 `_shared/skills` 与各非 `_` 前缀目录的 `skills/`。
	extraPrefixes []string
	// defaultMaturity 按 source 标签返回缺省成熟度；nil 时 _shared=frozen，其余=experimental。
	defaultMaturity func(source string) string
}

// WithExtraPrefix 追加扫描前缀（如 "custom/skills"）。
func WithExtraPrefix(p string) LoadOption {
	return func(c *loadConfig) { c.extraPrefixes = append(c.extraPrefixes, p) }
}

// WithDefaultMaturity 覆盖缺省成熟度推断。
func WithDefaultMaturity(f func(source string) string) LoadOption {
	return func(c *loadConfig) { c.defaultMaturity = f }
}

// LoadFromFS 扫描 FS 构建技能库。
// 布局：`_shared/skills/<名>/SKILL.md`（共享）与 `<包>/skills/<名>/SKILL.md`（包自带）。
// 名字取目录名，全局唯一；重名报错（fail-fast）。
func LoadFromFS(fsys fs.FS, opts ...LoadOption) (*Library, error) {
	cfg := &loadConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.defaultMaturity == nil {
		cfg.defaultMaturity = func(source string) string {
			if source == "_shared" {
				return MaturityFrozen
			}
			return MaturityExperimental
		}
	}

	lib := &Library{byName: map[string]LibEntry{}}
	sourceOf := func(prefix string) string {
		return strings.TrimSuffix(prefix, "/skills")
	}
	collect := func(prefix string) error {
		sub, err := fs.ReadDir(fsys, prefix)
		if err != nil {
			return nil // 目录不存在=无技能
		}
		for _, d := range sub {
			if !d.IsDir() {
				continue
			}
			name := d.Name()
			p := path.Join(prefix, name, "SKILL.md")
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				continue
			}
			if _, dup := lib.byName[name]; dup {
				return fmt.Errorf("skill: 技能名重复: %s（%s）", name, p)
			}
			meta, body, err := ParseRichFrontmatter(name, string(data))
			if err != nil {
				return fmt.Errorf("skill: %s: %w", p, err)
			}
			meta.Source = sourceOf(prefix)
			meta.Path = p
			if meta.Maturity == "" {
				meta.Maturity = cfg.defaultMaturity(meta.Source)
			}
			if meta.Version == "" {
				meta.Version = DefaultVersion
			}
			if meta.Mode == "" {
				meta.Mode = ModeStatic
			}
			if err := meta.Validate(); err != nil {
				return fmt.Errorf("skill: %s: %w", p, err)
			}
			lib.byName[name] = LibEntry{Full: string(data), Body: body, Meta: meta}
		}
		return nil
	}

	prefixes := []string{path.Join("_shared", "skills")}
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), "_") {
			prefixes = append(prefixes, path.Join(d.Name(), "skills"))
		}
	}
	prefixes = append(prefixes, cfg.extraPrefixes...)

	for _, prefix := range prefixes {
		if err := collect(prefix); err != nil {
			return nil, err
		}
	}
	return lib, nil
}

// Get 取技能全文（含 frontmatter）。
func (l *Library) Get(name string) (string, bool) {
	if l == nil {
		return "", false
	}
	e, ok := l.byName[name]
	return e.Full, ok
}

// Body 取剥离 frontmatter 的正文。
func (l *Library) Body(name string) (string, bool) {
	if l == nil {
		return "", false
	}
	e, ok := l.byName[name]
	return e.Body, ok
}

// Has 技能是否存在。
func (l *Library) Has(name string) bool {
	if l == nil {
		return false
	}
	_, ok := l.byName[name]
	return ok
}

// Describe 技能元数据（未知名字返回仅含名字的零值）。
func (l *Library) Describe(name string) LibMeta {
	if l == nil {
		return LibMeta{Name: name}
	}
	return l.byName[name].Meta
}

// Names 全部技能名。
func (l *Library) Names() []string {
	if l == nil {
		return nil
	}
	out := make([]string, 0, len(l.byName))
	for n := range l.byName {
		out = append(out, n)
	}
	return out
}

// Resolve 实现 Provider（决策式加载：剥 frontmatter 正文 + checksum）。
func (l *Library) Resolve(_ context.Context, ref Ref) (*Skill, error) {
	if l == nil {
		return nil, fmt.Errorf("skill: 技能库未装配")
	}
	e, ok := l.byName[ref.Name]
	if !ok {
		return nil, fmt.Errorf("skill: 技能 %q 不存在", ref.Name)
	}
	sum := sha256.Sum256([]byte(e.Body))
	return &Skill{
		Name:        ref.Name,
		Version:     e.Meta.Version,
		Description: e.Meta.Description,
		Content:     e.Body,
		Checksum:    hex.EncodeToString(sum[:])[:16],
	}, nil
}

// ListSkills 实现 Lister（渐进披露清单，不含正文）。
func (l *Library) ListSkills(_ context.Context) ([]Meta, error) {
	if l == nil {
		return nil, nil
	}
	out := make([]Meta, 0, len(l.byName))
	for n, e := range l.byName {
		out = append(out, Meta{
			Name:        n,
			Title:       e.Meta.Title,
			Version:     e.Meta.Version,
			Description: e.Meta.Description,
		})
	}
	return out, nil
}

// CanonicalName 实现 AliasResolver：frontmatter name（展示名）→ 目录名。
func (l *Library) CanonicalName(name string) (string, bool) {
	if l == nil {
		return "", false
	}
	if _, ok := l.byName[name]; ok {
		return name, true
	}
	for n, e := range l.byName {
		if e.Meta.Title == name {
			return n, true
		}
	}
	return "", false
}

// Provider 返回自身作为决策式 Provider（热替换场景每次读当前库）。
func (l *Library) Provider() Provider { return l }

// 编译期断言。
var (
	_ Provider      = (*Library)(nil)
	_ Lister        = (*Library)(nil)
	_ AliasResolver = (*Library)(nil)
)

// ParseRichFrontmatter 用 yaml 解析 SKILL.md frontmatter（name/description/mode/
// version/maturity/requires_mcp/deprecated/compatibility）。返回元数据与正文。
// frontmatter name 写入 Title（展示名）；目录名仍作规范 Name。
func ParseRichFrontmatter(dirName, content string) (LibMeta, string, error) {
	meta := LibMeta{Name: dirName, Mode: ModeStatic}
	body := content
	if !strings.HasPrefix(content, "---") {
		return meta, body, nil
	}
	inner := content[3:]
	rest := strings.TrimPrefix(inner, "\n")
	// 空 frontmatter：`---\n---` 经剥壳后 rest 以 --- 开头（无前置换行）。
	if strings.HasPrefix(rest, "---") {
		after := strings.TrimPrefix(rest, "---")
		if after == "" || after[0] == '\n' || after[0] == '\r' {
			return meta, strings.TrimSpace(strings.TrimPrefix(after, "\n")), nil
		}
	}
	// 定位独立成行的闭合 ---。
	closeAt := -1
	for off := 0; ; {
		j := strings.Index(rest[off:], "\n---")
		if j < 0 {
			break
		}
		pos := off + j
		endLine := pos + 4 // 指向 \n--- 之后
		if endLine >= len(rest) || rest[endLine] == '\n' || rest[endLine] == '\r' {
			closeAt = pos
			break
		}
		off = pos + 1
	}
	if closeAt < 0 {
		return meta, body, fmt.Errorf("frontmatter 未闭合")
	}
	fmRaw := rest[:closeAt]
	after := strings.TrimPrefix(rest[closeAt+1:], "---")
	body = strings.TrimSpace(after)

	var typed struct {
		Name           string      `yaml:"name"`
		Description    string      `yaml:"description"`
		Mode           string      `yaml:"mode"`
		Version        string      `yaml:"version"`
		Maturity       string      `yaml:"maturity"`
		RequiresMCP    []MCPDep    `yaml:"requires_mcp"`
		RequiresConfig []string    `yaml:"requires_config"`
		Deprecated     *Deprecated `yaml:"deprecated"`
		Provides       []string    `yaml:"provides"`
		Compatibility  string      `yaml:"compatibility"`
	}
	if err := yaml.Unmarshal([]byte(fmRaw), &typed); err != nil {
		return meta, body, fmt.Errorf("frontmatter YAML 非法: %w", err)
	}
	if typed.Name != "" {
		meta.Title = typed.Name
	}
	meta.Description = typed.Description
	if typed.Mode != "" {
		meta.Mode = typed.Mode
	}
	meta.Version = typed.Version
	meta.Maturity = typed.Maturity
	meta.RequiresMCP = typed.RequiresMCP
	meta.RequiresConfig = typed.RequiresConfig
	meta.Deprecated = typed.Deprecated
	meta.Provides = typed.Provides
	meta.Compatibility = typed.Compatibility
	return meta, body, nil
}

// DeprecatedExpiredInUse 列出「弃用窗口已过且仍被 used 引用」的技能名（调用方的
// reload/lint 失败清单）。used 为 技能名 → 引用方列表；未引用的过期弃用不阻塞加载。
func (l *Library) DeprecatedExpiredInUse(used map[string][]string, now time.Time) []string {
	if l == nil {
		return nil
	}
	var out []string
	for name, e := range l.byName {
		if len(used[name]) == 0 {
			continue
		}
		if e.Meta.DeprecationExpired(now) {
			out = append(out, name)
		}
	}
	return out
}
