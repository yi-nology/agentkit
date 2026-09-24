package acpx

import "time"

// RunEvent 一次 agent 运行的观测事件（Registry.OnRun 钩子载荷）。
type RunEvent struct {
	Agent     string        // agent 名
	Duration  time.Duration // 运行耗时
	Err       error         // 非空 = 运行失败
	TextLen   int           // 产出文本长度（不落内容，防泄露）
	Usage     Usage
	SessionID string
}
