// Package sast 确定性 SAST 前置扫描（收编自 argus internal/sastscan，v0.10.39，
// 对标 CodeRabbit 的 Semgrep/Snyk 集成）：在工作副本上执行 gitleaks / semgrep，
// 产出归一为结构化 Finding——全程零 LLM，作为独立信号注入嵌入方的合并/裁决层。
//
// 安全纪律（供应链）：二进制路径只来自部署方信任源（服务级配置），仓库级配置
// 不可启用/替换；gitleaks 全程 --redact 且 Secret/Commit 字段绝不进 Finding
// （密钥原文不进任何下游分发面）；子进程环境走白名单（EnvAllow，叠加在
// procx 基础集之上），不继承宿主密钥。
package sast

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yi-nology/agentkit/procx"
	"github.com/yi-nology/agentkit/reportutil"
	"github.com/yi-nology/agentkit/textutil"
)

// Config SAST 工具配置（bin 路径来自部署方信任源；全空 = 功能关闭）。
type Config struct {
	GitleaksBin   string        // gitleaks 绝对路径
	SemgrepBin    string        // semgrep 绝对路径
	SemgrepConfig string        // semgrep --config 值（路径/registry 名）
	Timeout       time.Duration // 单工具超时（0 = 120s）
	EnvAllow      []string      // 子进程环境白名单（按名透传当前进程变量；nil = 仅 procx 基础集）
}

// Enabled 任一工具 bin 已配置即启用。
func (c Config) Enabled() bool { return c.GitleaksBin != "" || c.SemgrepBin != "" }

// TimeoutResolved 生效超时。
func (c Config) TimeoutResolved() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 120 * time.Second
}

// Logger 最小日志口（logx.Logger 满足）。
type Logger interface {
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
}

// Finding SAST 单条产出（与嵌入方 finding 契约解耦，嵌入方自行映射）。
// Severity 为 reportutil 词表（high/medium/low）。
type Finding struct {
	Tool     string // gitleaks | semgrep
	File     string
	Line     int
	RuleID   string // gitleaks 规则 ID / semgrep check_id
	Severity string
	Comment  string // 人读评语（已含工具名前缀）
}

// ToolResult 单工具扫描结果（失败置 Err 不中断其它工具）。
type ToolResult struct {
	Tool     string
	Findings []Finding
	Notes    string // 截断等非致命告警
	Err      error
}

// maxFindingsPerTool 单工具 findings 封顶（防全仓扫描刷爆合并层）。
const maxFindingsPerTool = 50

// Scan 执行全部已配置工具，每工具产出一个 ToolResult。dir 为工作副本根；
// baseRef 非空时 gitleaks 按 origin/<base>..HEAD 提交范围扫描（工作副本已
// deepen 到 merge-base），为空退化为全仓 --no-git 扫描。paths 为本次审查实际
// 覆盖的变更文件清单（diff 聚焦——全仓扫描在大仓上既慢又淹没信噪比；nil/空 =
// 全仓）。未配置 config 的 semgrep 不产结果（不算覆盖缺口，只告警）。
func Scan(ctx context.Context, cfg Config, dir, baseRef string, paths []string, log Logger) []ToolResult {
	if !cfg.Enabled() {
		return nil
	}
	timeout := cfg.TimeoutResolved()
	var out []ToolResult
	if cfg.GitleaksBin != "" {
		out = append(out, runGitleaks(ctx, cfg, dir, baseRef, timeout, log))
	}
	if cfg.SemgrepBin != "" {
		if strings.TrimSpace(cfg.SemgrepConfig) == "" {
			log.Warn("sast.semgrep_no_config",
				"hint", "semgrep bin 已配但 config 为空，semgrep 跳过")
		} else {
			out = append(out, runSemgrep(ctx, cfg, dir, paths, timeout, log))
		}
	}
	return out
}

// runGitleaks gitleaks detect（JSON 报告落临时文件后解析）。
func runGitleaks(ctx context.Context, cfg Config, dir, baseRef string,
	timeout time.Duration, log Logger) ToolResult {

	res := ToolResult{Tool: "gitleaks", Findings: []Finding{}}
	tmp, err := os.CreateTemp("", "sast-gitleaks-*.json")
	if err != nil {
		res.Err = fmt.Errorf("创建报告临时文件失败: %w", err)
		return res
	}
	reportPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(reportPath)

	logOpts := ""
	if baseRef != "" {
		logOpts = "origin/" + baseRef + "..HEAD"
	}
	args := append(GitleaksArgs(dir, logOpts), "--report-path", reportPath)
	stdout, stderr, code, err := procx.Run(ctx, procx.RunRequest{
		Argv:    append([]string{cfg.GitleaksBin}, args...),
		Dir:     dir,
		Env:     cfg.EnvAllow,
		Timeout: timeout,
	})
	if err != nil {
		res.Err = fmt.Errorf("gitleaks 执行失败: %v: %s", err, truncLine(stderr, 300))
		return res
	}
	if code != 0 {
		res.Err = fmt.Errorf("gitleaks 退出码 %d: %s", code, truncLine(stdout+"\n"+stderr, 300))
		return res
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		res.Err = fmt.Errorf("读取 gitleaks 报告失败: %w", err)
		return res
	}
	var entries []struct {
		RuleID      string `json:"RuleID"`
		Description string `json:"Description"`
		File        string `json:"File"`
		StartLine   int    `json:"StartLine"`
		// Secret / Commit 刻意不消费：密钥原文绝不进 Finding（--redact 之外的
		// 第二道防线——评论是比日志更广的分发面）。
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		res.Err = fmt.Errorf("gitleaks 报告解析失败: %w", err)
		return res
	}
	for i, e := range entries {
		if i >= maxFindingsPerTool {
			res.Notes = fmt.Sprintf("gitleaks 产出超 %d 条已截断", maxFindingsPerTool)
			break
		}
		res.Findings = append(res.Findings, Finding{
			Tool: "gitleaks", File: e.File, Line: e.StartLine,
			Severity: reportutil.High, RuleID: e.RuleID,
			Comment: fmt.Sprintf("疑似硬编码敏感信息（gitleaks 规则 %s）：请将凭证移出源码并轮换已泄漏密钥。", e.RuleID),
		})
	}
	log.Info("sast.gitleaks_done", "findings", len(res.Findings))
	return res
}

// GitleaksArgs 命令行构造（logOpts 非空走提交范围扫描——--no-git 与
// --log-opts 互斥，真机 gitleaks 以 --log-opts 优先）。
func GitleaksArgs(dir, logOpts string) []string {
	if logOpts != "" {
		return []string{"detect", "--source", dir,
			"--report-format", "json", "--redact", "--exit-code", "0",
			"--log-opts", logOpts}
	}
	return []string{"detect", "--source", dir, "--no-git",
		"--report-format", "json", "--redact", "--exit-code", "0"}
}

// semgrepMaxPaths 聚焦扫描的文件数上限（防巨型 PR 撑爆 argv；超出回退全仓——
// 变更面已经足够大，聚焦收益归零）。
const semgrepMaxPaths = 100

// runSemgrep semgrep scan --json（dir 为扫描根；paths 非空时聚焦变更文件）。
func runSemgrep(ctx context.Context, cfg Config, dir string, paths []string,
	timeout time.Duration, log Logger) ToolResult {

	res := ToolResult{Tool: "semgrep", Findings: []Finding{}}
	args := []string{"scan", "--json", "--quiet", "--config", cfg.SemgrepConfig}
	if focused := FocusPaths(paths); len(focused) > 0 {
		args = append(args, focused...)
	} else {
		args = append(args, ".")
	}
	stdout, stderr, code, err := procx.Run(ctx, procx.RunRequest{
		Argv:    append([]string{cfg.SemgrepBin}, args...),
		Dir:     dir,
		Env:     cfg.EnvAllow,
		Timeout: timeout,
	})
	if err != nil || code != 0 {
		res.Err = fmt.Errorf("semgrep 执行失败: exit=%d err=%v: %s", code, err, truncLine(stdout+"\n"+stderr, 300))
		return res
	}
	var parsed struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
			Extra struct {
				Severity string `json:"severity"`
				Message  string `json:"message"`
			} `json:"extra"`
		} `json:"results"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		res.Err = fmt.Errorf("semgrep 输出解析失败: %w", err)
		return res
	}
	for i, r := range parsed.Results {
		if i >= maxFindingsPerTool {
			res.Notes = fmt.Sprintf("semgrep 产出超 %d 条已截断", maxFindingsPerTool)
			break
		}
		res.Findings = append(res.Findings, Finding{
			Tool: "semgrep", File: r.Path, Line: r.Start.Line,
			Severity: SemgrepSeverity(r.Extra.Severity), RuleID: r.CheckID,
			Comment: fmt.Sprintf("静态分析（semgrep %s）：%s", r.CheckID, r.Extra.Message),
		})
	}
	log.Info("sast.semgrep_done", "findings", len(res.Findings), "errors", len(parsed.Errors))
	return res
}

// FocusPaths 聚焦文件清单整形（存在性不做校验——以 diff 清单为准，缺失文件
// semgrep 自行忽略；>semgrepMaxPaths 回退 nil = 全仓；含 ".." 的路径剔除防
// 路径穿越出工作副本）。
func FocusPaths(paths []string) []string {
	if len(paths) == 0 || len(paths) > semgrepMaxPaths {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || strings.Contains(p, "..") {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SemgrepSeverity semgrep 三档（ERROR/WARNING/其余）→ reportutil 词表。
func SemgrepSeverity(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ERROR":
		return reportutil.High
	case "WARNING":
		return reportutil.Medium
	default:
		return reportutil.Low
	}
}

// truncLine 错误输出截断（进 Err 链，最终上报告警）。
func truncLine(s string, n int) string {
	return textutil.TruncEllipsis(strings.TrimSpace(s), n)
}
