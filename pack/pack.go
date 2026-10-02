// Package pack 领域包（expert/pack 目录布局）的契约面：布局约定为
// `_shared/`（平台共享基线）+ 各包目录（`<包>/`，`_` 前缀目录不算包），
// LayoutDirs 是该约定的单一事实源（本包的 mcp/ 扫描与 skill.Library 的
// skills/ 扫描共用——布局演进单点修改）。本包承载跨包的 MCP 工具面契约清单
// ——加载规则（基线/整文件覆盖/字典序冲突）为唯一事实源，调用方
// （reload 校验、lint、血缘）共享同一份加载语义。
package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LayoutBaseline `_shared` 基线目录名。
const LayoutBaseline = "_shared"

// LayoutDirs 领域包布局约定的单一事实源：返回基线目录名与全部包目录名
// （`_` 前缀目录不算包），包名按字典序返回（确定性装配序契约）。
func LayoutDirs(fsys fs.FS) (baseline string, packs []string, err error) {
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", nil, err
	}
	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), "_") {
			packs = append(packs, d.Name())
		}
	}
	sort.Strings(packs)
	return LayoutBaseline, packs, nil
}

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
	l := &manifestLoader{fsys: fsys, out: map[string]*ToolManifest{}}
	// _shared 基线先入；包按字典序覆盖（与领域包装配的确定性排序契约一致）。
	baseline, packs, err := LayoutDirs(fsys)
	if err != nil {
		return nil, nil, err
	}
	if err := l.parseDir(path.Join(baseline, "mcp"), baseline); err != nil {
		return nil, nil, err
	}
	for _, pack := range packs {
		if err := l.parseDir(path.Join(pack, "mcp"), pack); err != nil {
			return nil, nil, err
		}
	}
	return l.out, l.warns, nil
}

// manifestLoader 清单装载过程状态（结果表 + 冲突警告）。
type manifestLoader struct {
	fsys  fs.FS
	out   map[string]*ToolManifest
	warns []string
}

// parseDir 解析单目录下的 *.yaml 清单；目录不存在=无清单（合法布局形态）。
// 其余 FS 故障（权限/IO）上抛——静默吞掉会让血缘对账面静默退化且无任何信号
// （与同包 LayoutDirs 对根目录错误的显式上抛同纪律；第六轮审计）。
func (l *manifestLoader) parseDir(dir, owner string) error {
	sub, err := fs.ReadDir(l.fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("tool_manifest: 读取目录 %s 失败: %w", dir, err)
	}
	for _, f := range sub {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
			continue
		}
		server := strings.TrimSuffix(f.Name(), ".yaml")
		m, err := readManifest(l.fsys, dir, f.Name(), server, owner)
		if err != nil {
			return err
		}
		l.merge(server, owner, m)
	}
	return nil
}

// readManifest 读盘并解析单个清单文件。
func readManifest(fsys fs.FS, dir, file, server, owner string) (*ToolManifest, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, file))
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, file, err)
	}
	var m ToolManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s/%s 解析: %w", dir, file, err)
	}
	m.Server, m.From = server, owner
	return &m, nil
}

// merge 合入结果表：包覆盖 _shared 基线合法；包间同名冲突警告 + 先到先得。
func (l *manifestLoader) merge(server, owner string, m *ToolManifest) {
	prev, dup := l.out[server]
	if !dup {
		l.out[server] = m
		return
	}
	if prev.From == LayoutBaseline {
		// 包覆盖 _shared 基线：合法整文件替换。
		l.out[server] = m
		return
	}
	// 包间同名冲突：警告 + 字典序第一生效（迭代按字典序，先到先得）。
	l.warns = append(l.warns, fmt.Sprintf("tool_manifest %q 在 %s 与 %s 同名冲突，按包名字典序 %s 生效",
		server, prev.From, owner, prev.From))
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
