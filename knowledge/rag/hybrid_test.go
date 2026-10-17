// Hybrid 混合检索测试（R10）：RRF 跨路共识加权/去重/确定性、topK 截断、单路失败降级、
// 双路全败回错、AsTool 透传语义路。
package rag

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

type fakeKS struct {
	chunks []Chunk
	err    error
}

func (f *fakeKS) Retrieve(_ context.Context, _ string, topK int, _ Filter) ([]Chunk, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.chunks) > topK {
		return f.chunks[:topK], nil
	}
	return f.chunks, nil
}
func (f *fakeKS) AsTool() tool.BaseTool { return nil }

// panicKS Retrieve 即恐慌的语义路桩（GoSafe 收编验证）。
type panicKS struct{}

func (panicKS) Retrieve(context.Context, string, int, Filter) ([]Chunk, error) {
	panic("semantic lane boom")
}
func (panicKS) AsTool() tool.BaseTool { return nil }

func chunk(id, content string) Chunk { return Chunk{ID: id, Content: content} }

func TestFuseRRFConsensusBoost(t *testing.T) {
	// doc-a 双路命中（共识）应排在单路命中之前——即使向量路里 doc-b 排第一。
	vector := []Chunk{chunk("b", "向量路第一"), chunk("a", "向量路第二")}
	keyword := []Chunk{chunk("a", "词料路第一")}
	got := FuseRRF(5, vector, keyword)
	if len(got) != 2 {
		t.Fatalf("应 2 条: %d", len(got))
	}
	if got[0].ID != "a" {
		t.Fatalf("共识 doc-a 应第一: %+v", got)
	}
	// 共识得分=2/(k+1)+1/(k+2) 量级必须高于单路第一 1/(k+1)
	if got[0].Score == 0 {
		t.Fatal("RRF 得分未回填（Score 字段应携带融合分供观测）")
	}
}

func TestFuseRRFDedupeAndDeterminism(t *testing.T) {
	vector := []Chunk{chunk("x", "内容副本长版本"), chunk("x", "短")}
	keyword := []Chunk{chunk("x", "中等副本内容")}
	got := FuseRRF(5, vector, keyword)
	if len(got) != 1 {
		t.Fatalf("同 ID 应去重: %d", len(got))
	}
	if got[0].Content != "内容副本长版本" {
		t.Fatalf("代表副本应取信息量最大: %q", got[0].Content)
	}
	// 同分确定性：两独立文档同 rank → ID 字典序
	a := FuseRRF(5, []Chunk{chunk("z", "1")}, []Chunk{chunk("y", "2")})
	if len(a) != 2 || a[0].ID != "y" || a[1].ID != "z" {
		t.Fatalf("同分应按 ID 字典序: %+v", a)
	}
}

// fakeLocal/two helper：真 Local 索引临时目录（词料路走真实 TF-IDF）。
func fakeLocal(t *testing.T) *Local {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "k.md"), []byte("kubernetes 集群异常排查步骤\n先看节点状态再看 Pod 事件"), 0o644)
	l, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Rescan()
	return l
}

func fakeLocalErr(t *testing.T) *Local {
	// Local 无注入错误缝——用指向不存在文件的目录模拟「词料路不可用」形态：
	// NewLocal 对不存在目录报错，这里构造空目录（Retrieve 返回空=降级面等效）。
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestHybridDegradation(t *testing.T) {
	// 语义路失败 → 降级词料路（查询须命中词料内容——TF-IDF 无匹配返回空属正常检索结果）
	h := NewHybrid(&fakeKS{err: errors.New("milvus down")}, fakeLocal(t))
	got, err := h.Retrieve(context.Background(), "kubernetes", 3, nil)
	if err != nil || len(got) == 0 {
		t.Fatalf("单路失败应降级: %v %+v", err, got)
	}
	// 词料路失败 → 降级语义路
	h2 := NewHybrid(&fakeKS{chunks: []Chunk{chunk("v1", "向量结果")}}, fakeLocalErr(t))
	got2, err := h2.Retrieve(context.Background(), "q", 3, nil)
	if err != nil || len(got2) == 0 {
		t.Fatalf("另一向失败应降级: %v %+v", err, got2)
	}
	// 双路全败 → 回 first error
	h3 := NewHybrid(&fakeKS{err: errors.New("a")}, fakeLocalErr(t))
	if _, err := h3.Retrieve(context.Background(), "q", 3, nil); err == nil {
		t.Fatal("双路全败应回错")
	}
	// topK 截断
	h4 := NewHybrid(&fakeKS{chunks: []Chunk{chunk("1", "a"), chunk("2", "b"), chunk("3", "c")}}, fakeLocal(t))
	got4, err := h4.Retrieve(context.Background(), "q", 1, nil)
	if err != nil || len(got4) != 1 {
		t.Fatalf("topK 截断不符: %v %+v", err, got4)
	}
}

func TestHybridAsToolDelegates(t *testing.T) {
	h := NewHybrid(&fakeKS{}, fakeLocal(t))
	if h.AsTool() != nil {
		t.Fatal("应透传语义路 AsTool（fake 返回 nil）")
	}
}

func TestHybridLanePanicDegrades(t *testing.T) {
	// 语义路 panic → FanInErr Slot.Panicked 记为该路失败，降级词料路（恐慌不出宿主进程）
	h := NewHybrid(panicKS{}, fakeLocal(t))
	got, err := h.Retrieve(context.Background(), "kubernetes", 3, nil)
	if err != nil || len(got) == 0 {
		t.Fatalf("恐慌路应降级另一路: %v %+v", err, got)
	}
	// 语义路 panic + 词料路空命中（无错无结果）→ 双路皆无产出，panic 作为失败回错，
	// 而非 FanIn 时代把 panic 伪装成空路后静默返回 (nil, nil)
	_, err = h.Retrieve(context.Background(), "zzz-no-such-term-qqq", 3, nil)
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("恐慌路无降级可用时应回 panic 错误而非静默空结果: %v", err)
	}
}
