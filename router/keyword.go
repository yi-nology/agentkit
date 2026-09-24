// 中文词表匹配内核：意图路由的确定性层（词表兜底优先于 LLM 分类）。
//
// 子串命中远远不够——「磁盘没有问题」包含"磁盘"、「不要执行」包含"执行"，
// 直配会把查询/拒绝误判成命中。本文件在子串之上叠加三重守门：
//  1. 否定守门：命中处紧前方窗口内出现否定短语即该处作废（中文否定习惯前置，只回看不前看）；
//  2. 词边界：拉丁词命中处紧邻字符不得是 ASCII 字母/数字——kill 不再误命中 skill/killed；
//  3. 排除构式：命中位置落在排除短语（「怎么执行」「可执行文件」）内部即该处作废。
//
// 三重守门都只作废**该处**命中：同一输入里关键词多处出现时，任一处通过全部守门即命中
// （「先看下执行结果，怎么执行再定」的两处都被构式排除才判 false）。
//
// 从 bianque 意图路由层提炼（v0.9.4），语言层通用、无领域词。
package router

import "strings"

// negPhrases 否定前置短语表：关键词紧前方窗口内出现即该处命中作废。
// 只收复合短语不收单字（「无」「没」「未」单字会误伤「无网络时看磁盘」这类正常表述）。
var negPhrases = []string{
	"没有", "不是", "并非", "并无", "不存在", "未发现", "未出现", "无异常", "不要", "不用",
}

// negWindow 否定守门回看窗口（字节，15≈5 个汉字）：覆盖「没有检测到磁盘」这类
// 否定词与关键词之间的常见修饰间隔。只回看不前看——中文否定习惯前置。
const negWindow = 15

// OnNegationHit 否定守门命中钩子（可选；观测面注册，如 metrics 计数）。
// 在「该处命中被否定作废」时回调——热路径上只在作废瞬间触发，零命中零开销。
// 构建期注册（无内部同步）。
var OnNegationHit func()

// KeywordHit 词表命中判定：子串匹配 + 否定前置守门——「磁盘没有问题」「不要执行」
// 不再因包含关键词而误命中；同一输入里关键词多处出现时，任一处未被否定即命中。
// 输入应已小写（中文不受影响；拉丁关键词内部自动 ToLower 兜底）。
func KeywordHit(lowerInput, keyword string) bool {
	return hitScan(lowerInput, keyword, nil, false)
}

// KeywordHitBoundary KeywordHit 的词边界变体：命中处的前一字符与后一字符都不得是
// ASCII 字母/数字（CJK 与标点均视为边界）——为「kill」这类拉丁词设计，杜绝
// 「skill」「killed」误命中；中文关键词不受影响（相邻字符本就不是 ASCII 字母数字，
// 行为与 KeywordHit 完全一致，混排输入如「ssh执行」也不受牵连——只约束声明的词）。
func KeywordHitBoundary(lowerInput, keyword string) bool {
	return hitScan(lowerInput, keyword, nil, true)
}

// KeywordHitBoundaryExcept KeywordHitBoundary 的排除构式变体（词边界 + mask 构式 +
// 否定守门三重判定）。
func KeywordHitBoundaryExcept(lowerInput, keyword string, masks []string) bool {
	return hitScan(lowerInput, keyword, masks, true)
}

// KeywordHitExcept KeywordHit 的排除构式变体：关键词某处命中若落在任一 mask 复合短语
// （如「怎么执行」「可执行文件」）内部，该处作废、继续找下一处；全部作废即 false。
// 否定守门与 KeywordHit 一致。语义：处置词出现在问句/名词性构式里不触发执行链，
// 而同句真正的处置词（kill）不受「执行结果」这类名词性短语牵连。
func KeywordHitExcept(lowerInput, keyword string, masks []string) bool {
	return hitScan(lowerInput, keyword, masks, false)
}

// hitScan 逐处扫描关键词命中（四个导出入口的统一骨架）：任一处通过全部守门即命中；
// 否定守门/词边界/排除构式都只作废**该处**，不作废整条词。
// boundary=true 叠加 ASCII 词边界判定（拉丁词防 skill/killed 误命中）。
func hitScan(lowerInput, keyword string, masks []string, boundary bool) bool {
	kw := strings.ToLower(keyword)
	for start := 0; ; {
		i := strings.Index(lowerInput[start:], kw)
		if i < 0 {
			return false
		}
		pos := start + i
		neg := negatedAt(lowerInput, pos)
		masked := maskedAt(lowerInput, pos, len(kw), masks)
		// 观测面：否定作为作废原因时计数（命中已被构式排除的，不归功否定守门）
		if neg && !masked && OnNegationHit != nil {
			OnNegationHit()
		}
		if !neg && !masked && (!boundary || !boundaryFail(lowerInput, pos, pos+len(kw))) {
			return true
		}
		start = pos + len(kw) // 该处被作废，继续找下一处
	}
}

// boundaryFail 命中区间的紧邻字符是否为 ASCII 字母/数字（是则视为混入更长单词，作废该处）。
func boundaryFail(lower string, pos, end int) bool {
	if pos > 0 {
		c := lower[pos-1]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return true
		}
	}
	if end < len(lower) {
		c := lower[end]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return true
		}
	}
	return false
}

// maskedAt pos..pos+kwLen 的关键词命中是否落在任一 mask 短语某处出现的内部。
func maskedAt(lower string, pos, kwLen int, masks []string) bool {
	for _, m := range masks {
		mm := strings.ToLower(m)
		for s := 0; ; {
			i := strings.Index(lower[s:], mm)
			if i < 0 {
				break
			}
			mpos := s + i
			if pos >= mpos && pos+kwLen <= mpos+len(mm) {
				return true
			}
			s = mpos + 1
		}
	}
	return false
}

// postNegChars 后置否定单字表：命中词紧后方出现即视为否定陈述。紧贴判定有意不收
// 复合短语——复合否定形态由前置守门（negPhrases 窗口回看）覆盖，紧贴单字是谓词否定
// （「负载不高」）的专属形态。
var postNegChars = []string{"不", "没", "无", "非"}

// KeywordPostNegated 后置否定守门：关键词**任一处**命中后紧随否定单字（不/没/无/非）
// 即视为否定陈述——「负载不高」「磁盘不满」里命中词是被否定的谓词对象；多处出现时
// 逐一扫描（与其余守门「任一处」语义对齐：「磁盘没问题，再看下磁盘」不会因首处未被
// 否定而漏判第二处的否定形态）。紧随复合否定（「磁盘没有问题」）同命中。与否定前置
// 守门（negatedAt）对偶：那边中文否定习惯前置、按窗口回看，这边紧贴即判、无需窗口。
// 判定从严：宁可作废误归一，不放过反向表述。返回 true 表示该关键词应作废。
// 输入应已小写（关键词内部 ToLower 兜底）。
func KeywordPostNegated(lowerInput, keyword string) bool {
	kw := strings.ToLower(keyword)
	for start := 0; ; {
		i := strings.Index(lowerInput[start:], kw)
		if i < 0 {
			return false
		}
		pos := start + i
		rest := lowerInput[pos+len(kw):]
		for _, n := range postNegChars {
			if strings.HasPrefix(rest, n) {
				return true
			}
		}
		start = pos + len(kw)
	}
}

// negatedAt 判断 lower[pos:] 处的关键词命中是否被紧前方窗口内的否定短语作废。
// 疑问构式（有没有/是不是/要不要）字面包含否定短语——先剥离再判，否则
// 「检查有没有后门」「看看是不是有漏洞」会被误判为否定。
// 剥离窗口额外向前扩 1 个汉字（3 字节）：构式可能横跨 15 字节窗口边界
// （「要不要做一次弱口令检测」的不在窗内、要在窗外）。
func negatedAt(lower string, pos int) bool {
	from := pos - negWindow - 3
	if from < 0 {
		from = 0
	}
	w := strings.ReplaceAll(lower[from:pos], "有没有", "有有")
	w = strings.ReplaceAll(w, "是不是", "是是")
	w = strings.ReplaceAll(w, "要不要", "要要")
	for _, n := range negPhrases {
		if strings.Contains(w, n) {
			return true
		}
	}
	return false
}
