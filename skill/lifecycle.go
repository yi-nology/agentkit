package skill

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	dateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Validate 元数据硬校验（加载期 fail-fast；错误信息带「字段: 消息」定位）。
//   - mode/maturity 枚举；version 须为 SemVer（缺省 0.0.0 合法）；
//   - maturity=deprecated 必须带 deprecated.remove_after（YYYY-MM-DD）——弃用必须有窗口终点；
//   - requires_mcp[].server 非空。
func (m LibMeta) Validate() error {
	switch m.Mode {
	case ModeStatic, ModeOnDemand:
	default:
		return fmt.Errorf("mode 非法 %q（static|on_demand）", m.Mode)
	}
	switch m.Maturity {
	case MaturityExperimental, MaturityStable, MaturityFrozen, MaturityDeprecated:
	default:
		return fmt.Errorf("maturity 非法 %q（experimental|stable|frozen|deprecated）", m.Maturity)
	}
	if m.Version != "" && m.Version != DefaultVersion && !semverRe.MatchString(m.Version) {
		return fmt.Errorf("version 非法 %q（SemVer）", m.Version)
	}
	if m.Maturity == MaturityDeprecated {
		if m.Deprecated == nil || m.Deprecated.RemoveAfter == "" {
			return fmt.Errorf("maturity=deprecated 必须填写 deprecated.remove_after（YYYY-MM-DD）")
		}
		if !dateRe.MatchString(m.Deprecated.RemoveAfter) {
			return fmt.Errorf("deprecated.remove_after 非法 %q（YYYY-MM-DD）", m.Deprecated.RemoveAfter)
		}
	}
	for i, dep := range m.RequiresMCP {
		if dep.Server == "" {
			return fmt.Errorf("requires_mcp[%d].server 不能为空", i)
		}
	}
	return nil
}

// RewriteMode 结构化切换 mode：解析 frontmatter → 改 mode → yaml 序列化写回，
// 正文与其余元数据字段（嵌套 requires_mcp/provides 等）原样保留。
// 无 frontmatter 或未闭合 → 报错（不猜格式，与解析 fail-fast 同哲学）。
func RewriteMode(content, mode string) (string, error) {
	if mode != ModeStatic && mode != ModeOnDemand {
		return "", fmt.Errorf("mode 非法 %q（static|on_demand）", mode)
	}
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("SKILL.md 无 frontmatter，无法切换 mode")
	}
	meta, body, err := ParseRichFrontmatter("tmp", content)
	if err != nil {
		return "", err
	}
	meta.Mode = mode
	fm, err := marshalFrontmatter(meta)
	if err != nil {
		return "", err
	}
	if body == "" {
		return fm + "\n", nil
	}
	return fm + "\n\n" + body + "\n", nil
}

// RewriteBody 保留 frontmatter 围栏（原文不动），仅替换正文——编辑器路径。
// 无 frontmatter 或未闭合 → 报错（围栏是元数据的唯一保护壳，不整段重建）。
func RewriteBody(content, body string) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("SKILL.md 无 frontmatter，无法编辑正文")
	}
	lines := strings.Split(content, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", fmt.Errorf("frontmatter 未闭合，拒绝编辑")
	}
	kept := strings.Join(lines[:end+1], "\n")
	return kept + "\n\n" + strings.TrimRight(body, "\n") + "\n", nil
}

// marshalFrontmatter 序列化回 SKILL.md frontmatter（只写已知字段；Title 还原为
// frontmatter name，缺省 version 0.0.0 不写回——避免文件被无意义膨胀）。
func marshalFrontmatter(m LibMeta) (string, error) {
	out := struct {
		Name           string      `yaml:"name,omitempty"`
		Description    string      `yaml:"description,omitempty"`
		Mode           string      `yaml:"mode"`
		Version        string      `yaml:"version,omitempty"`
		Maturity       string      `yaml:"maturity,omitempty"`
		RequiresMCP    []MCPDep    `yaml:"requires_mcp,omitempty"`
		RequiresConfig []string    `yaml:"requires_config,omitempty"`
		Deprecated     *Deprecated `yaml:"deprecated,omitempty"`
		Provides       []string    `yaml:"provides,omitempty"`
		Compatibility  string      `yaml:"compatibility,omitempty"`
	}{
		Name:           m.Title,
		Description:    m.Description,
		Mode:           m.Mode,
		Version:        m.Version,
		Maturity:       m.Maturity,
		RequiresMCP:    m.RequiresMCP,
		RequiresConfig: m.RequiresConfig,
		Deprecated:     m.Deprecated,
		Provides:       m.Provides,
		Compatibility:  m.Compatibility,
	}
	if out.Version == DefaultVersion {
		out.Version = ""
	}
	b, err := yaml.Marshal(&out)
	if err != nil {
		return "", err
	}
	return "---\n" + string(b) + "---", nil
}
