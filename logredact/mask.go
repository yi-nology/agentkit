package logredact

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Masker 低敏感掩码+令牌回填器（K8sGPT anonymize 形态，bianque spec
// 2026-09-14-aiops-uplift-design §3）：把目标机拓扑标识（IPv4 + 调用方注册的精确串）
// 在进 LLM 前替换为 «Tn» 令牌，在最终展示面 Restore 回填原名——LLM 解释里引用的
// 「主机 «T1»」在报告中还原为真名，可读性与隐私兼得。
//
// 分级纪律：高敏感（密码/密钥/连接串/Bearer）不走本机制——那是 Redact 的领域，
// 删除后永不回填；Masker 只收低敏感拓扑标识，且回填只发生在展示面、不回灌二次
// LLM 输入（防掩码词表被 prompt 注入探测绕过）。令牌用全角书名号+序号，难以仿造；
// Restore 对未知令牌原样保留（不猜、不误回填）。
type Masker struct {
	mu      sync.Mutex
	next    int
	tokens  map[string]string // token → 原文
	origins map[string]string // 原文 → token（注册去重）
}

// NewMasker 构造（零值不可用，一律经本构造）。
func NewMasker() *Masker {
	return &Masker{tokens: map[string]string{}, origins: map[string]string{}}
}

// ipv4Re 低敏感默认模式：IPv4 地址（工具输出里最常见的拓扑标识）。
var ipv4Re = regexp.MustCompile(`\b(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}\b`)

// tokenRe 令牌形态（Restore 只认本包签发的全角书名号+序号）。
var tokenRe = regexp.MustCompile(`«T([0-9]+)»`)

// AddSecret 注册精确串（会话主机名、实例名等调用方已知标识），返回其令牌。空串忽略。
func (m *Masker) AddSecret(v string) string {
	if v == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maskLocked(v)
}

// maskLocked 注册原文→令牌（去重；同原文恒同令牌）。须持锁调用。
func (m *Masker) maskLocked(v string) string {
	if tok, ok := m.origins[v]; ok {
		return tok
	}
	m.next++
	tok := "«T" + strconv.Itoa(m.next) + "»"
	m.origins[v] = tok
	m.tokens[tok] = v
	return tok
}

// Mask 掩码文本：先精确注册串，再 IPv4 模式（动态注册，同址恒同令牌）。
func (m *Masker) Mask(s string) string {
	if s == "" {
		return s
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for origin, tok := range m.origins {
		if origin != "" {
			s = strings.ReplaceAll(s, origin, tok)
		}
	}
	s = ipv4Re.ReplaceAllStringFunc(s, func(match string) string {
		return m.maskLocked(match)
	})
	return s
}

// Restore 回填令牌为原文（展示面专用；未知令牌原样保留）。
func (m *Masker) Restore(s string) string {
	if s == "" {
		return s
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return tokenRe.ReplaceAllStringFunc(s, func(tok string) string {
		if v, ok := m.tokens[tok]; ok {
			return v
		}
		return tok
	})
}

// Count 已签发令牌数（测试/观测用）。
func (m *Masker) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.next
}
