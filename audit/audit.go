// Package audit 审计日志：对敏感操作留结构化审计痕迹，与业务日志分离。
package audit

import (
	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// Action 审计操作类型（调用方按需扩展）。
type Action string

// Logger 审计日志器。
type Logger struct {
	log logx.Logger
}

// New 创建审计日志器。service 用于日志前缀（如 "argus-audit"）。
// log 为 nil 时回退到缺省 slog logger——审计留痕不应随业务日志级别配置丢失。
func New(log logx.Logger, service string) *Logger {
	if log == nil {
		log = logx.NewSlogLogger(service)
	}
	return &Logger{log: log.WithService(service)}
}

// Log 记录一条审计事件（nil receiver 安全）。
func (l *Logger) Log(action Action, fields ...any) {
	if l == nil || l.log == nil {
		return
	}
	args := append([]any{"action", string(action)}, fields...)
	l.log.Info("audit.event", args...)
}
