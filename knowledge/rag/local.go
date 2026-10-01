package rag

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"

	"github.com/yi-nology/agentkit/textutil"
	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

const (
	defaultTopK      = 5
	chunkTargetRunes = 600
	rescanInterval   = 10 * time.Minute
	toolSnippetRunes = 800
	overlapRunes     = 100  // 块间重叠字符数，保持上下文连续性
	maxChunkRunes    = 4000 // 块硬上限：粘贴 base64/超长日志、未闭合代码块保护 Milvus VarChar 65535 字节
)

type indexedChunk struct {
	chunk     Chunk
	tokenFreq map[string]int
}

// Local 本地文件知识库（启动扫描 + 过期重扫）。
type Local struct {
	dir string

	mu        sync.Mutex
	index     []indexedChunk
	idf       map[string]float64 // token → smoothed IDF（rescan 预计算，检索时零 log）
	scannedAt time.Time
}

// NewLocal 构造并立即扫描。dir 不存在时返回错误。
func NewLocal(dir string) (*Local, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("rag: %s 不是目录", dir)
	}
	l := &Local{dir: dir}
	l.Rescan()
	return l, nil
}

// Retrieve 检索 topK 片段。
// 重扫在锁外进行（构建新索引后原子换入）——并发检索不被文件 I/O 阻塞；
// 多 goroutine 同时判过期会重复重扫，幂等无害，容忍。
func (l *Local) Retrieve(ctx context.Context, query string, topK int, filter Filter) ([]Chunk, error) {
	_ = ctx // 当前实现无阻塞点，保留 ctx 以面向未来（接口契约）
	if err := validateFilter(filter); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if time.Since(l.scannedAt) >= rescanInterval {
		l.mu.Unlock()
		l.Rescan()
		l.mu.Lock()
	}
	idx, idf := l.index, l.idf
	l.mu.Unlock()

	if topK <= 0 {
		topK = defaultTopK
	}
	qt := tokenize(query)
	if len(qt) == 0 || len(idx) == 0 {
		return nil, nil
	}
	return topKChunks(idx, idf, qt, topK, filter), nil
}

// scored 堆元素（检索路径局部类型，不外泄）。
type scored struct {
	c indexedChunk
	s float64
}

// siftDown 小顶堆下沉维护（堆顶=当前第 K 大）。
func siftDown(heap []scored, i int) {
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(heap) && heap[l].s < heap[m].s {
			m = l
		}
		if r < len(heap) && heap[r].s < heap[m].s {
			m = r
		}
		if m == i {
			return
		}
		heap[i], heap[m] = heap[m], heap[i]
		i = m
	}
}

// siftUp 小顶堆上浮维护。
func siftUp(heap []scored, i int) {
	for i > 0 {
		p := (i - 1) / 2
		if heap[i].s < heap[p].s {
			heap[i], heap[p] = heap[p], heap[i]
			i = p
		} else {
			return
		}
	}
}

// topKChunks 固定容量小顶堆选 topK：堆顶是当前第 K 大，全程 O(chunks·log K)，
// 替代"全量收集 + 全排序"（大 hits 切片分配是检索路径内存开销的大头）。
func topKChunks(idx []indexedChunk, idf map[string]float64, qt []string, topK int, filter Filter) []Chunk {
	heap := make([]scored, 0, topK)
	for _, ic := range idx {
		if !matchFilter(ic.chunk.Metadata, filter) {
			continue
		}
		s := scoreTFIDF(ic.tokenFreq, qt, idf)
		if s <= 0 {
			continue
		}
		if len(heap) < topK {
			heap = append(heap, scored{ic, s})
			siftUp(heap, len(heap)-1)
		} else if s > heap[0].s {
			heap[0] = scored{ic, s}
			siftDown(heap, 0)
		}
	}
	sort.Slice(heap, func(i, j int) bool { return heap[i].s > heap[j].s })
	out := make([]Chunk, 0, len(heap))
	for _, h := range heap {
		out = append(out, h.c.chunk)
	}
	return out
}

// AsTool 把检索包成 search_knowledge 工具。
func (l *Local) AsTool() tool.BaseTool {
	return buildAsTool(l)
}

// Rescan 强制重新扫描知识库目录（不等待自动过期）。
// 根目录不可达（卷卸载/被删）时保留旧索引并告警——静默换入空索引会让
// 知识库"消失"且无任何信号。
func (l *Local) Rescan() {
	if _, err := os.Stat(l.dir); err != nil {
		logx.NewSlogLogger("agentkit-rag").Warn("agentkit.rag.rescan_root_missing", "dir", l.dir, "error", err.Error())
		return
	}
	index, df, walkErr := l.scanMarkdown()
	idf := computeIDF(len(index), df)
	// 原子换入：检索侧要么看到完整旧索引、要么完整新索引，不见中间态。
	// 全量读失败（walkErr 非空且一无所获）时保留旧索引——区分"用户清空目录"
	// （WalkDir 无 err，正常换入空索引）与"读失败"（保留旧索引）。
	l.mu.Lock()
	if walkErr != nil && len(index) == 0 {
		l.mu.Unlock()
		return
	}
	l.index, l.idf, l.scannedAt = index, idf, time.Now()
	l.mu.Unlock()
}

// scanMarkdown 遍历目录构建分块索引与文档频率表。
// WalkDir 对"已处理的错误"返回 nil 即跳过继续——但根目录 stat 通过而不可 readdir
// （权限收紧/挂载半故障）时整棵树读不到，静默换入空索引 = 知识库"消失"且无信号。
// 记录读取错误：全部读失败时保留旧索引（与根目录缺失同策略），部分失败告警后照常换入。
func (l *Local) scanMarkdown() (index []indexedChunk, df map[string]int, walkErr error) {
	df = map[string]int{}
	_ = filepath.WalkDir(l.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if walkErr == nil {
				walkErr = err
			}
			logx.NewSlogLogger("agentkit-rag").Warn("agentkit.rag.rescan_entry_error", "path", path, "error", err.Error())
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // 单文件不可读跳过，不阻塞整体索引
		}
		rel, _ := filepath.Rel(l.dir, path)
		indexMarkdownFile(rel, string(data), &index, df)
		return nil
	})
	return index, df, walkErr
}

// indexMarkdownFile 单文件分块入索引并累计文档频率。
func indexMarkdownFile(rel, data string, index *[]indexedChunk, df map[string]int) {
	for _, c := range chunkMarkdown(data) {
		toks := tokenize(c.content)
		freq := make(map[string]int, len(toks))
		for _, t := range toks {
			freq[t]++
		}
		for t := range freq { // 文档频率顺手统计，省一次全索引遍历
			df[t]++
		}
		*index = append(*index, indexedChunk{
			chunk: Chunk{
				Content:  c.content,
				Metadata: map[string]string{"file": rel, "heading": c.heading},
			},
			tokenFreq: freq, // 打分只用词频表；词元切片不再保留（省一份内存）
		})
	}
}

// computeIDF smoothed IDF 预计算（1 + log(N/df)，最小值 1，避免单 chunk 时 IDF=0
// 导致零分）。检索路径零 log——此前每 chunk×查询词都要重算一遍。
func computeIDF(n int, df map[string]int) map[string]float64 {
	nf := float64(n)
	idf := make(map[string]float64, len(df))
	for t, d := range df {
		idf[t] = 1 + logF(nf/float64(d))
	}
	return idf
}

type mdChunk struct {
	heading string
	content string
}

// chunkMarkdown 按标题和空行分段，合并到 ~chunkTargetRunes，支持代码块保护。
func chunkMarkdown(text string) []mdChunk {
	var z mdChunker
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		z.handleLine(line, i+1 < len(lines))
	}
	z.flush()
	return z.chunks
}

// mdChunker 切块状态机（行级副作用集中一处，标题/代码块/重叠互不渗透）。
type mdChunker struct {
	chunks      []mdChunk
	heading     string
	cur         []string
	curLen      int
	inCodeBlock bool
}

// flush 当前累积行成块（无实质内容则丢弃）。
func (z *mdChunker) flush() {
	if len(z.cur) == 0 {
		return
	}
	content := strings.TrimSpace(strings.Join(z.cur, "\n"))
	if content != "" {
		z.chunks = append(z.chunks, mdChunk{heading: z.heading, content: content})
	}
	z.cur = z.cur[:0]
	z.curLen = 0
}

// carryOverlap 把 cur 末尾 overlap 带入下一块（标题切块与长度切块共用）。
func (z *mdChunker) carryOverlap() {
	tail := overlapTail(z.cur)
	z.flush()
	if tail != "" {
		z.cur = append(z.cur, tail)
		z.curLen = utf8.RuneCountInString(tail)
	}
}

// handleLine 单行入块：超长行守卫（正文/代码块共用）/ 代码块保护 / 标题切换 /
// 长度切块。more=是否还有后续行（末行到阈值只 flush，不带 overlap——没有下一
// 块可带入）。
func (z *mdChunker) handleLine(line string, more bool) {
	// 单行超限守卫前置：粘贴 base64/超长日志恰恰常包在代码围栏里，此前守卫
	// 只在正文路径——代码块内单条超长行直接越过 Milvus VarChar 65535 字节
	// 上限，flush 出的单块让整个文件（含正常段落）Insert 失败（第六轮审计
	// C 级）。围栏行不切（切了会破坏开/闭状态机配对）。
	if !isFenceLine(line) && utf8.RuneCountInString(line) > maxChunkRunes {
		z.handleOverlongLine(line)
		return
	}
	if isFenceLine(line) {
		z.inCodeBlock = !z.inCodeBlock
		z.appendLine(line)
		return
	}
	if z.inCodeBlock {
		z.appendLine(line)
		// 多行累积守卫：未闭合代码块 + 超长内容强制成块
		if z.curLen >= maxChunkRunes {
			z.flush()
		}
		return
	}
	if h, ok := mdHeading(line); ok {
		if len(z.cur) > 0 {
			z.carryOverlap()
		} else {
			z.flush()
		}
		z.heading = h
		return
	}
	z.handleBodyLine(line, more)
}

// isFenceLine 代码围栏行（``` 开头，可带语言标注）。
func isFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

// handleOverlongLine 超长行硬切（单一入口）：先收当前块，再按 maxChunkRunes
// rune 等分成多块（heading 上下文保留）。硬切丢行内格式优于截断丢内容，且
// 每块确定性 ≤ maxChunkRunes（此前正文路径截断、代码块路径不设防，两处漂移）。
func (z *mdChunker) handleOverlongLine(line string) {
	if len(z.cur) > 0 {
		z.flush()
	}
	for _, seg := range textutil.SplitRunes(line, maxChunkRunes) {
		z.chunks = append(z.chunks, mdChunk{heading: z.heading, content: seg})
	}
}

func (z *mdChunker) appendLine(line string) {
	z.cur = append(z.cur, line)
	z.curLen += utf8.RuneCountInString(line)
}

// handleBodyLine 正文行：达目标块长则切块（单行超限守卫已前置 handleLine）。
func (z *mdChunker) handleBodyLine(line string, more bool) {
	z.appendLine(line)
	if z.curLen >= chunkTargetRunes {
		if more {
			z.carryOverlap()
		} else {
			z.flush()
		}
	}
}

// overlapTail 取块文本末尾 overlapRunes 个字符（不足阈值返回 ""=不携带）。
func overlapTail(cur []string) string {
	if overlapRunes <= 0 || len(cur) == 0 {
		return ""
	}
	runes := []rune(strings.Join(cur, "\n"))
	if len(runes) <= overlapRunes {
		return ""
	}
	return string(runes[len(runes)-overlapRunes:])
}

func mdHeading(line string) (string, bool) {
	t := strings.TrimSpace(line)
	// 支持 # ## ### 三级标题
	for _, prefix := range []string{"### ", "## ", "# "} {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimPrefix(t, prefix), true
		}
	}
	return "", false
}

// tokenize 分词：ASCII 词（小写）+ CJK 二元组（单字成词时补单字）。
// 字节偏移迭代：CJK bigram 直接切原串子串（零拷贝），ASCII 词写入即小写
// （词内只可能是 ASCII 字母/数字，小写化只影响 A-Z），避免 []rune 全量拷贝。
func tokenize(s string) []string {
	var out []string
	var word []byte
	addWord := func() {
		if len(word) > 0 {
			out = append(out, string(word))
		}
		word = word[:0]
	}
	for i, r := range s {
		switch {
		case (r < 128 && unicode.IsLetter(r)) || unicode.IsDigit(r):
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			word = utf8.AppendRune(word, r)
		case unicode.Is(unicode.Han, r):
			addWord()
			size := utf8.RuneLen(r)
			end := i + size
			if end < len(s) {
				if r2, size2 := utf8.DecodeRuneInString(s[end:]); unicode.Is(unicode.Han, r2) {
					out = append(out, s[i:end+size2])
				} else {
					out = append(out, s[i:end])
				}
			} else {
				out = append(out, s[i:end])
			}
		default:
			addWord()
		}
	}
	addWord()
	return out
}

// scoreTFIDF TF-IDF 加权打分：sum(tf * idf[t]) / sqrt(queryLen)。
// idf 为 rescan 预计算表；tf>0 的 token 必在表中（该 chunk 含 t ⇒ df[t]≥1），
// 兜底 w==0 时取 1 作中性权重。
func scoreTFIDF(chunkFreq map[string]int, queryTokens []string, idf map[string]float64) float64 {
	if len(chunkFreq) == 0 || len(queryTokens) == 0 {
		return 0
	}
	var score float64
	for _, t := range queryTokens {
		tf := float64(chunkFreq[t])
		if tf == 0 {
			continue
		}
		w := idf[t]
		if w == 0 {
			w = 1
		}
		score += tf * w
	}
	if score == 0 {
		return 0
	}
	return score / sqrtF(float64(len(queryTokens)))
}

func logF(x float64) float64 {
	if x <= 1 {
		return 0
	}
	return math.Log(x)
}

// sqrtF 平方根（x<=0 时返回 1 作中性分母）。
func sqrtF(x float64) float64 {
	if x <= 0 {
		return 1
	}
	return math.Sqrt(x)
}

func matchFilter(meta map[string]string, filter Filter) bool {
	for k, v := range filter {
		if meta[k] != v {
			return false
		}
	}
	return true
}

// filterKeys 检索过滤字段白名单（包级单源：Local 与 Milvus 后端共用——此前
// 白名单只在 Milvus 侧，Local 对未知 key 静默返回空结果，同一接口两后端
// 行为相悖，配置错误被当成"没搜到"）。
var filterKeys = map[string]bool{"file": true, "heading": true}

// validateFilter 检索过滤字段校验：白名单外的 key 直接报错（fail-fast）。
func validateFilter(filter Filter) error {
	for k := range filter {
		if !filterKeys[k] {
			return fmt.Errorf("rag: 不支持的过滤字段 %q（可用: file, heading）", k)
		}
	}
	return nil
}
