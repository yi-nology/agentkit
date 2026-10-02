// Package pack 领域包（expert/pack 目录布局）的契约面：布局约定为
// `_shared/`（平台共享基线）+ 各包目录（`<包>/`，`_` 前缀目录不算包），
// 与 skill.Library 的扫描约定一致。本包承载跨包的 MCP 工具面契约清单
// ——加载规则（基线/整文件覆盖/字典序冲突）为唯一事实源，调用方
// （reload 校验、lint、血缘）共享同一份加载语义。
package pack

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestTool 清单声明的单工具（契约面；desc 仅供人读）。
type ManifestTool struct {
	Name string `yaml:"name" json:"name"`
	Desc string `yaml:"desc" json:"desc,omitempty"`
}

// ToolManifest MCP server 工具面契约清单（`_shared/mcp/<server>.yaml` 或
// `<包>/mcp/<server>.yaml`）。conf（运行时装配）是装配事实源，清单是契约事实源；
// 两者对账由调用方做（缺清单=现状语义：授予反查推导，不对账）。
type ToolManifest struct {
	Server      string         `yaml:"-" json:"-"` // 文件名（<server>.yaml），加载期填
	Name        string         `yaml:"name" json:"name"`
	Version     string         `yaml:"version" json:"version,omitempty"`
	Description string         `yaml:"description" json:"description,omitempty"`
	Tools       []ManifestTool `yaml:"tools" json:"tools"`
	From        string         `yaml:"-" json:"from,omitempty"` // 生效来源（"_shared" 或包名；同名词冲突提示用）
}

// LoadToolManifests 扫描 FS 的契约清单：`_shared/mcp/*.yaml` 基线 + 各包
// `mcp/*.yaml` 覆盖。规则：
//   - 目录不存在=无清单（合法，恒返回非 nil map）；
//   - 包清单整文件替换 _shared 基线（不是 merge——「扩展清单」应拷出全量再增改）；
//   - 包间同名冲突 → 警告 + 按包名字典序第一个生效（确定性、可测试；
//     两包真争同一 server 的工具面属治理问题，警告暴露给人裁决）。
func LoadToolManifests(fsys fs.FS) (map[string]*ToolManifest, []string, error) {
	out := map[string]*ToolManifest{}
	var warns []string
	parse := func(dir, owner string) error {
		sub, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return nil // 目录不存在=无清单，合法
		}
		for _, f := range sub {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			server := strings.TrimSuffix(f.Name(), ".yaml")
			raw, err := fs.ReadFile(fsys, path.Join(dir, f.Name()))
			if err != nil {
				return fmt.Errorf("%s/%s: %w", dir, f.Name(), err)
			}
			var m ToolManifest
			if err := yaml.Unmarshal(raw, &m); err != nil {
				return fmt.Errorf("%s/%s 解析: %w", dir, f.Name(), err)
			}
			m.Server, m.From = server, owner
			if prev, dup := out[server]; dup {
				if prev.From == "_shared" {
					// 包覆盖 _shared 基线：合法整文件替换。
					out[server] = &m
					continue
				}
				// 包间同名冲突：警告 + 字典序第一生效（迭代按字典序，先到先得）。
				warns = append(warns, fmt.Sprintf("tool_manifest %q 在 %s 与 %s 同名冲突，按包名字典序 %s 生效",
					server, prev.From, owner, prev.From))
				continue
			}
			out[server] = &m
		}
		return nil
	}
	// _shared 基线先入；包按字典序覆盖（与领域包装配的确定性排序契约一致）。
	if err := parse(path.Join("_shared", "mcp"), "_shared"); err != nil {
		return nil, nil, err
	}
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, err
	}
	var packs []string
	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), "_") {
			packs = append(packs, d.Name())
		}
	}
	sort.Strings(packs)
	for _, pack := range packs {
		if err := parse(path.Join(pack, "mcp"), pack); err != nil {
			return nil, nil, err
		}
	}
	return out, warns, nil
}

// Has 声明是否包含某工具名。
func (m *ToolManifest) Has(tool string) bool {
	if m == nil {
		return false
	}
	for _, t := range m.Tools {
		if t.Name == tool {
			return true
		}
	}
	return false
}
