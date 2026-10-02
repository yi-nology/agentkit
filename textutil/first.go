package textutil

// FirstNonEmpty 返回首个非空串（全空返回 ""）。多候选回退语义单源
// （模型/配置/平台字段逐级兜底的高频形态）。
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
