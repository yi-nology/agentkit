package skill

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDeclUnmarshalForms 裸串与映射混排（agent.yaml skills / pack.yaml depends_on.skills 形态）。
func TestDeclUnmarshalForms(t *testing.T) {
	var v struct {
		Skills []Decl `yaml:"skills"`
	}
	raw := "skills:\n  - alpha\n  - {name: beta, version: \">=1.0.0\", optional: true}\n"
	if err := yaml.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(v.Skills) != 2 {
		t.Fatalf("skills=%+v", v.Skills)
	}
	if v.Skills[0].Name != "alpha" || v.Skills[0].Version != "" || v.Skills[0].Optional {
		t.Fatalf("裸串引用解析错误: %+v", v.Skills[0])
	}
	if v.Skills[1].Name != "beta" || v.Skills[1].Version != ">=1.0.0" || !v.Skills[1].Optional {
		t.Fatalf("映射引用解析错误: %+v", v.Skills[1])
	}
	// 非法形态：空串 / 映射缺 name。
	for _, bad := range []string{"skills: [\"  \"]\n", "skills:\n  - {version: \"1.0.0\"}\n"} {
		var w struct {
			Skills []Decl `yaml:"skills"`
		}
		if err := yaml.Unmarshal([]byte(bad), &w); err == nil {
			t.Fatalf("应报错: %s", bad)
		}
	}
}
