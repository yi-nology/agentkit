package skill

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decl 技能的结构化声明引用（agent.yaml skills / pack.yaml depends_on.skills 等
// 装配声明的列表元素）。兼容两种 yaml 形态：裸串（=仅名字，无约束）与
// {name, version, optional} 映射。version 是 semver 区间表达式
// （">=1.0.0 <2.0.0"，空格=AND）；空=不约束。Optional=true 表示引用缺失时
// 允许警告降级（是否降级由装配方决定，本包不做存在性检查）。
type Decl struct {
	Name     string `yaml:"name" json:"name"`
	Version  string `yaml:"version,omitempty" json:"version,omitempty"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// UnmarshalYAML 兼容裸串与映射两种形态（yaml.v3）。
func (d *Decl) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		d.Name = strings.TrimSpace(s)
		if d.Name == "" {
			return fmt.Errorf("skill 引用为空")
		}
		return nil
	}
	type plain Decl
	var v plain
	if err := value.Decode(&v); err != nil {
		return err
	}
	if v.Name == "" {
		return fmt.Errorf("skill 引用缺 name")
	}
	*d = Decl(v)
	return nil
}
