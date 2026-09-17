// Package clarify 澄清/标准化词表内核：口语→规范维度词条（term_map）的模型、外置
// 加载、内在校验，以及澄清反问的回答消解（序数指代 → 选项词包含 → 逃生兜底）。
//
// 归一/澄清只补充语义不改路由：词表命中产出宿主槽位（词表治本，LLM 同名槽位不可
// 推翻）；挂起态存取、反问状态机与注入段渲染留宿主——本包只管「表长什么样、回答
// 怎么消解」这两件纯逻辑。
//
// 沉淀自 bianque 输入标准化层 + 消歧 clarify（v0.9.7）：序数解析在宿主已有消歧衔接
// 与标准化反问两处消费者。
package clarify

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options 澄清反问模板：Question 提问、Options 可选答案（声明序=序数序）、
// Fallback 逃生选项（「不清楚，全面查」——选它=放弃细分走全量）。
type Options struct {
	Question string   `yaml:"question"`
	Options  []string `yaml:"options"`
	Fallback string   `yaml:"fallback"`
}

// Entry 词表条目：Word 由宿主词表内核子串匹配（如 router.KeywordHit，含否定守门），
// 声明序即匹配优先级——specific 词条须声明在泛词之前。
type Entry struct {
	Word      string   `yaml:"word"`
	Dimension string   `yaml:"dimension"` // 标准维度键 ^[a-z][a-z0-9_]*$
	Terms     []string `yaml:"terms"`     // 标准术语（宿主注入段展示用）
	Vague     bool     `yaml:"vague"`     // true=模糊维度：宿主场景命中时可反问补齐细分
	Clarify   *Options `yaml:"clarify"`   // vague=true 时必填；非 vague 不得配置
	Domain    string   `yaml:"domain"`    // 空=全域；否则宿主校验其为已注册域
}

// LoadVocab 读 <fsys>/<path> 外置词表（yaml 根键 term_map）。缺文件返回 (nil, nil)
// （零词表=宿主零行为变化）；存在但读取/解析失败即报错（拒绝半份词表静默上线）。
func LoadVocab(fsys fs.FS, path string) ([]Entry, error) {
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	var doc struct {
		TermMap []Entry `yaml:"term_map"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s 解析: %w", path, err)
	}
	return doc.TermMap, nil
}

// dimRe 标准维度键规范：小写拉丁开头，仅小写字母/数字/下划线。
var dimRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate 词表内在校验：word 非空且唯一、维度键规范、vague⟺clarify 完整
// （question/options/fallback 必带，逃生选项必配）。域注册存在性与路由词冲突等
// 宿主拓扑校验不在此——宿主在装配期自校（缺信息，也无从给出准确告警文案）。
func Validate(entries []Entry) error {
	owner := map[string]string{}
	for i := range entries {
		e := &entries[i]
		if e.Word == "" {
			return fmt.Errorf("term_map 第 %d 条缺 word", i+1)
		}
		if prev, hit := owner[e.Word]; hit {
			return fmt.Errorf("term_map 词 %q 重复（%s / %s）", e.Word, prev, e.Dimension)
		}
		owner[e.Word] = e.Dimension
		if !dimRe.MatchString(e.Dimension) {
			return fmt.Errorf("term_map %q 维度键非法 %q（须 ^[a-z][a-z0-9_]*$）", e.Word, e.Dimension)
		}
		if e.Vague && (e.Clarify == nil || e.Clarify.Question == "" || len(e.Clarify.Options) == 0 || e.Clarify.Fallback == "") {
			return fmt.Errorf("term_map %q vague=true 须配 clarify{question,options,fallback}（逃生选项必带）", e.Word)
		}
		if !e.Vague && e.Clarify != nil {
			return fmt.Errorf("term_map %q 非 vague 不得配置 clarify", e.Word)
		}
	}
	return nil
}

// OrdinalIndex 解析回答里的序数指代（第一个/第1个/第 2 个/1./选项二/选一），返回 1-based
// 序号。汉字与阿拉伯数字双形态；仅当剥离序数构式后剩余部分为空、整体就是序数指代时
// 命中（「1个进程在跑」这类普通句子不误伤）。
func OrdinalIndex(lower string) (int, bool) {
	s := strings.TrimSpace(lower)
	if s == "" {
		return 0, false
	}
	s = strings.ReplaceAll(s, " ", "")
	for _, p := range []string{"选项", "选", "第"} {
		s = strings.TrimPrefix(s, p)
	}
	for _, suf := range []string{"个", "选项", "方案", "的", ".", "。", "、"} {
		s = strings.TrimSuffix(s, suf)
	}
	if n := len([]rune(s)); n != 1 {
		return 0, false
	}
	digits := map[rune]int{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
		'1': 1, '2': 2, '3': 3, '4': 4, '5': 5, '6': 6, '7': 7, '8': 8, '9': 9}
	r := []rune(s)[0]
	n, ok := digits[r]
	return n, ok
}

// ResolveAnswer 澄清回答消解：序数指代（1-based 对 options 序，越界=放弃）→ 选项词
// 包含匹配（双侧去空格，容「磁盘io」对「磁盘 IO」）→ 逃生选项（答案是其前缀也算，
// 如「不清楚」对「不清楚，全面查」）。
// picked=命中的选项词（fallback 命中时为空）；ok=false=回答不在本题语义内（宿主按
// 「用户换话题」处置，清挂起走正常分类）。
func ResolveAnswer(options []string, fallback, answer string) (picked string, isFallback bool, ok bool) {
	lower := strings.TrimSpace(strings.ToLower(answer))
	if lower == "" {
		return "", false, false
	}
	if n, ok := OrdinalIndex(lower); ok {
		if n >= 1 && n <= len(options) {
			return options[n-1], false, true
		}
		return "", false, false
	}
	strip := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "") }
	a := strip(answer)
	if a == "" {
		return "", false, false
	}
	for _, o := range options {
		if strings.Contains(a, strip(o)) {
			return o, false, true
		}
	}
	fb := strip(fallback)
	if fb != "" && (strings.Contains(a, fb) || strings.Contains(fb, a)) {
		return "", true, true
	}
	return "", false, false
}
