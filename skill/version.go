package skill

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// VersionInRange 版本是否满足区间表达式（技能/依赖的声明式约束）：区间语法与
// Masterminds/semver 一致（">=1.0.0 <2.0.0" 空格=AND，逗号亦同）。校验语义
// （失败/警告分级）由调用方定——本函数只回答「满足/不满足 + 表达式本身是否合法」。
// expr 空=任意（恒真）。
// 区间或版本本身非法时返回错误，错误信息带期望/实际。
func VersionInRange(version, expr string) (bool, error) {
	if strings.TrimSpace(expr) == "" {
		return true, nil
	}
	c, err := semver.NewConstraint(expr)
	if err != nil {
		return false, fmt.Errorf("skill: 版本区间非法 %q: %w", expr, err)
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return false, fmt.Errorf("skill: 版本 %q 非法（SemVer）: %w", version, err)
	}
	return c.Check(v), nil
}
