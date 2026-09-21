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

	// 固定容量小顶堆选 topK：堆顶是当前第 K 大，全程 O(chunks·log K)，
	// 替代"全量收集 + 全排序"（大 hits 切片分配是检索路径内存开销的大头）
	type scored struct {
		c indexedChunk
		s float64
	}
	heap := make([]scored, 0, topK)
	siftDown := func(i int) {
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
			for i := len(heap) - 1; i > 0; {
				p := (i - 1) / 2
				if heap[i].s < heap[p].s {
					heap[i], heap[p] = heap[p], heap[i]
					i = p
				} else {
					break
				}
			}
		} else if s > heap[0].s {
			heap[0] = scored{ic, s}
			siftDown(0)
		}
	}
	sort.Slice(heap, func(i, j int) bool { return heap[i].s > heap[j].s })
	out := make([]Chunk, 0, len(heap))
	for _, h := range heap {
		out = append(out, h.c.chunk)
	}
	return out, nil
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
	var index []indexedChunk
	df := map[string]int{}
	// WalkDir 对"已处理的错误"返回 nil 即跳过继续——但根目录 stat 通过而不可 readdir
	// （权限收紧/挂载半故障）时整棵树读不到，静默换入空索引 = 知识库"消失"且无信号。
	// 记录读取错误：全部读失败时保留旧索引（与根目录缺失同策略），部分失败告警后照常换入。
	var walkErr error
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
		for _, c := range chunkMarkdown(string(data)) {
			toks := tokenize(c.content)
			freq := make(map[string]int, len(toks))
			for _, t := range toks {
				freq[t]++
			}
			for t := range freq { // 文档频率顺手统计，省一次全索引遍历
				df[t]++
			}
			index = append(index, indexedChunk{
				chunk: Chunk{
					Content:  c.content,
					Metadata: map[string]string{"file": rel, "heading": c.heading},
				},
				tokenFreq: freq, // 打分只用词频表；词元切片不再保留（省一份内存）
			})
		}
		return nil
	})
	// IDF 预计算：smoothed IDF（1 + log(N/df)，最小值 1，避免单 chunk 时 IDF=0 导致零分）。
	// 检索路径零 log——此前每 chunk×查询词都要重算一遍。
	n := float64(len(index))
	idf := make(map[string]float64, len(df))
	for t, d := range df {
		idf[t] = 1 + logF(n/float64(d))
	}
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

type mdChunk struct {
	heading string
	content string
}

// chunkMarkdown 按标题和空行分段，合并到 ~chunkTargetRunes，支持代码块保护。
func chunkMarkdown(text string) []mdChunk {
	var chunks []mdChunk
	heading := ""
	var cur []string
	curLen := 0
	inCodeBlock := false

	flush := func() {
		if len(cur) == 0 {
			return
		}
		content := strings.TrimSpace(strings.Join(cur, "\n"))
		if content != "" {
			chunks = append(chunks, mdChunk{heading: heading, content: content})
		}
		cur = cur[:0]
		curLen = 0
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// 代码块保护：不拆分 ``` 内部
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeBlock = !inCodeBlock
			cur = append(cur, line)
			curLen += utf8.RuneCountInString(line)
			continue
		}
		if inCodeBlock {
			cur = append(cur, line)
			curLen += utf8.RuneCountInString(line)
			// 硬上限保护：未闭合代码块 + 超长内容强制成块，避免单块超
			// Milvus VarChar 65535 字节导致整个文件 Insert 失败
			if curLen >= maxChunkRunes {
				flush()
			}
			continue
		}

		if h, ok := mdHeading(line); ok {
			// 保留重叠：把上一块的最后 overlapRunes 个字符带入新块
			if len(cur) > 0 && overlapRunes > 0 {
				all := strings.Join(cur, "\n")
				runes := []rune(all)
				if len(runes) > overlapRunes {
					overlap := string(runes[len(runes)-overlapRunes:])
					flush()
					cur = append(cur, overlap)
					curLen = len([]rune(overlap))
				} else {
					flush()
				}
			} else {
				flush()
			}
			heading = h
			continue
		}

		// 硬上限保护：单行超限（粘贴 base64/日志）强制截断
		if lineRunes := utf8.RuneCountInString(line); lineRunes > maxChunkRunes {
			if len(cur) > 0 {
				flush()
			}
			chunks = append(chunks, mdChunk{heading: heading,
				content: textutil.TruncNote(line, maxChunkRunes, "超长行截断")})
			continue
		}
		cur = append(cur, line)
		curLen += utf8.RuneCountInString(line)
		if curLen >= chunkTargetRunes {
			// 保留尾部 overlap 到下一块
			if overlapRunes > 0 && i+1 < len(lines) {
				all := strings.Join(cur, "\n")
				runes := []rune(all)
				if len(runes) > overlapRunes {
					tail := string(runes[len(runes)-overlapRunes:])
					flush()
					cur = append(cur, tail)
					curLen = len([]rune(tail))
				} else {
					flush()
				}
			} else {
				flush()
			}
		}
	}
	flush()
	return chunks
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
