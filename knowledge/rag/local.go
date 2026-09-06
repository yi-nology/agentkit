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

	"github.com/cloudwego/eino/components/tool"
)

const (
	defaultTopK      = 5
	chunkTargetRunes = 600
	rescanInterval   = 10 * time.Minute
	toolSnippetRunes = 800
	overlapRunes     = 100 // 块间重叠字符数，保持上下文连续性
)

type indexedChunk struct {
	chunk     Chunk
	tokens    []string
	tokenFreq map[string]int
}

// Local 本地文件知识库（启动扫描 + 过期重扫）。
type Local struct {
	dir string

	mu        sync.Mutex
	index     []indexedChunk
	df        map[string]int // token → 出现该 token 的 chunk 数（rescan 预计算，检索时零重建）
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
func (l *Local) Retrieve(_ context.Context, query string, topK int, filter Filter) ([]Chunk, error) {
	l.mu.Lock()
	stale := time.Since(l.scannedAt) >= rescanInterval
	l.mu.Unlock()
	if stale {
		l.Rescan()
	}
	l.mu.Lock()
	idx, df := l.index, l.df
	l.mu.Unlock()

	if topK <= 0 {
		topK = defaultTopK
	}
	qt := tokenize(query)
	if len(qt) == 0 || len(idx) == 0 {
		return nil, nil
	}

	n := float64(len(idx))
	type scored struct {
		c indexedChunk
		s float64
	}
	var hits []scored
	for _, ic := range idx {
		if !matchFilter(ic.chunk.Metadata, filter) {
			continue
		}
		s := scoreTFIDF(ic.tokenFreq, qt, df, n)
		if s > 0 {
			hits = append(hits, scored{ic, s})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	out := make([]Chunk, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.c.chunk)
	}
	return out, nil
}

// AsTool 把检索包成 search_knowledge 工具。
func (l *Local) AsTool() tool.BaseTool {
	return buildAsTool(l)
}


// Rescan 强制重新扫描知识库目录（不等待自动过期）。
func (l *Local) Rescan() {
	var index []indexedChunk
	df := map[string]int{}
	_ = filepath.WalkDir(l.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
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
			index = append(index, indexedChunk{
				chunk: Chunk{
					Content:  c.content,
					Metadata: map[string]string{"file": rel, "heading": c.heading},
				},
				tokens:    toks,
				tokenFreq: freq,
			})
		}
		return nil
	})
	// IDF 文档频率预计算：检索路径零重建（每查询 O(总词元) → O(1)）
	for _, ic := range index {
		for t := range ic.tokenFreq {
			df[t]++
		}
	}
	// 原子换入：检索侧要么看到完整旧索引、要么完整新索引，不见中间态
	l.mu.Lock()
	l.index, l.df, l.scannedAt = index, df, time.Now()
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
			curLen += len([]rune(line))
			continue
		}
		if inCodeBlock {
			cur = append(cur, line)
			curLen += len([]rune(line))
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

		cur = append(cur, line)
		curLen += len([]rune(line))
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
func tokenize(s string) []string {
	var out []string
	var word strings.Builder
	addWord := func() {
		if w := strings.ToLower(word.String()); w != "" {
			out = append(out, w)
		}
		word.Reset()
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case unicode.IsLetter(r) && r < 128 || unicode.IsDigit(r):
			word.WriteRune(r)
		case unicode.Is(unicode.Han, r):
			addWord()
			if i+1 < len(runes) && unicode.Is(unicode.Han, runes[i+1]) {
				out = append(out, string([]rune{r, runes[i+1]}))
			} else {
				out = append(out, string(r))
			}
		default:
			addWord()
		}
	}
	addWord()
	return out
}

// scoreTFIDF TF-IDF 加权打分：sum(tf * (1 + log(N/df))) / sqrt(queryLen)。
// 使用 smoothed IDF：1 + log(N/df)，避免单 chunk 时 IDF=0 导致零分。
func scoreTFIDF(chunkFreq map[string]int, queryTokens []string, df map[string]int, n float64) float64 {
	if len(chunkFreq) == 0 || len(queryTokens) == 0 {
		return 0
	}
	var score float64
	for _, t := range queryTokens {
		tf := float64(chunkFreq[t])
		if tf == 0 {
			continue
		}
		d := float64(df[t])
		if d == 0 {
			d = 1
		}
		idf := 1 + logF(n/d) // smoothed IDF，最小值为 1
		score += tf * idf
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

func sqrtF(x float64) float64 {
	if x <= 0 {
		return 1
	}
	g := x
	for i := 0; i < 4; i++ {
		g = (g + x/g) / 2
	}
	return g
}

func matchFilter(meta map[string]string, filter Filter) bool {
	for k, v := range filter {
		if meta[k] != v {
			return false
		}
	}
	return true
}
