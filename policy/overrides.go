package policy

import (
	"errors"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// DefaultPolicy 内置基座：auto 代批至 L2、full 代批至 L3（L4 恒人工双确认）。
func DefaultPolicy() *Policy {
	return &Policy{AutoMaxRisk: 2, FullMaxRisk: 3}
}

// LoadOverrides 读 <fsys>/<path> 外置策略文件（yaml 根键 operation_policy）。
// 缺文件 → 内置基座（nil error）；读取/解析失败 → error（fail-fast，启动即败，
// 宁拒不启动不带错跑）。阈值 clamp 到基座——外置文件只能调低不能调高
// （调高=扩权，须改基座走评审）。path 由宿主传入（如领域包布局约定
// "_shared/operation_policy.yaml"）。
func LoadOverrides(fsys fs.FS, path string) (*Policy, error) {
	base := DefaultPolicy()
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return base, nil
		}
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	var doc struct {
		Policy Policy `yaml:"operation_policy"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s 解析: %w", path, err)
	}
	p := &doc.Policy
	if p.AutoMaxRisk <= 0 || p.AutoMaxRisk > base.AutoMaxRisk {
		p.AutoMaxRisk = base.AutoMaxRisk
	}
	if p.FullMaxRisk <= 0 || p.FullMaxRisk > base.FullMaxRisk {
		p.FullMaxRisk = base.FullMaxRisk
	}
	return p, nil
}
