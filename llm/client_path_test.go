package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ---------- Client.Generate 重试主路径（Resilient 之外的原生路径） ----------

func TestClientGenerateRetryThenSuccess(t *testing.T) {
	m := &fakeChatModel{script: []fakeResp{
		{err: errors.New("500 internal error")},
		{err: errors.New("connection reset")},
		{content: "ok"},
	}}
	c := NewClient(m, "test", nil)
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 5 * time.Millisecond

	out, err := c.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatalf("Content = %q", out.Content)
	}
	if m.callCount() != 3 {
		t.Fatalf("应调 3 次（2 败 1 成），实际 %d", m.callCount())
	}
}

func TestClientGenerateNonRetryable(t *testing.T) {
	m := &fakeChatModel{script: []fakeResp{
		{err: errors.New("401 unauthorized")},
		{content: "should not reach"},
	}}
	c := NewClient(m, "test", nil)
	c.BaseDelay = time.Millisecond

	_, err := c.Generate(context.Background(), "R1", msgs("hi"))
	if err == nil {
		t.Fatal("401 应直接失败")
	}
	if m.callCount() != 1 {
		t.Fatalf("401 不应重试，实际调了 %d 次", m.callCount())
	}
}

func TestClientGenerateTruncationBoost(t *testing.T) {
	// 第一次输出被截断（finish_reason=length）→ 提升 MaxTokens 重试 → 第二次成功
	m := &fakeChatModel{script: []fakeResp{
		{content: "半截", finishReason: "length", prompt: 10, completion: 5},
		{content: "完整输出", prompt: 10, completion: 20},
	}}
	c := NewClient(m, "test", nil)
	c.BaseDelay = time.Millisecond
	c.MaxOutputTokens = 100

	out, err := c.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "完整输出" {
		t.Fatalf("Content = %q", out.Content)
	}
	if m.callCount() != 2 {
		t.Fatalf("截断应触发一次重试，实际 %d 次", m.callCount())
	}
}

func TestClientGenerateJSONRetryWithFeedback(t *testing.T) {
	// 第一次非法 JSON → 回喂错误重试 → 第二次合法
	m := &fakeChatModel{script: []fakeResp{
		{content: "不是 JSON"},
		{content: `{"a":1}`},
	}}
	c := NewClient(m, "test", nil)
	c.BaseDelay = time.Millisecond

	var out struct {
		A int `json:"a"`
	}
	if err := c.GenerateJSON(context.Background(), "R1", msgs("hi"), &out); err != nil {
		t.Fatal(err)
	}
	if out.A != 1 {
		t.Fatalf("A = %d", out.A)
	}
	if m.callCount() != 2 {
		t.Fatalf("JSON 回喂重试应调 2 次，实际 %d", m.callCount())
	}
}

func TestClientGenerateBudgetAccounting(t *testing.T) {
	m := &fakeChatModel{script: []fakeResp{
		{content: "ok", prompt: 100, completion: 50},
	}}
	budget := NewBudget(1000)
	c := NewClient(m, "test", budget)

	if _, err := c.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if budget.Used() != 150 {
		t.Fatalf("预算应记 150，实际 %d", budget.Used())
	}
	if c.UsedTokens() != 150 {
		t.Fatalf("UsedTokens = %d", c.UsedTokens())
	}
	if c.RawModel() != model.BaseChatModel(m) {
		t.Fatal("RawModel 应返回构造时传入的模型")
	}
}

func TestClientUsedTokensNilBudget(t *testing.T) {
	c := NewClient(&fakeChatModel{}, "test", nil)
	if c.UsedTokens() != 0 {
		t.Fatal("无预算时 UsedTokens 应为 0")
	}
}

func TestClientBackoffDelay(t *testing.T) {
	c := NewClient(nil, "test", nil)
	c.BaseDelay = 2 * time.Second
	c.MaxDelay = 30 * time.Second

	// 普通错误：指数增长
	d1 := c.backoffDelay(1, c.BaseDelay, c.MaxDelay, errors.New("500"))
	if d1 < 2*time.Second || d1 > 3*time.Second {
		t.Fatalf("attempt1 退避应约 2s+jitter，实际 %v", d1)
	}
	// 429：下限 5s
	d429 := c.backoffDelay(1, c.BaseDelay, c.MaxDelay, errors.New("429 rate limit"))
	if d429 < 5*time.Second {
		t.Fatalf("429 退避下限 5s，实际 %v", d429)
	}
	// 上限封顶
	dMax := c.backoffDelay(10, c.BaseDelay, c.MaxDelay, errors.New("500"))
	if dMax > 30*time.Second {
		t.Fatalf("退避应封顶 30s，实际 %v", dMax)
	}
}

// ---------- Generator 接口在 Client 上的行为 ----------

func TestClientGeneratorInterface(t *testing.T) {
	m := &fakeChatModel{script: []fakeResp{{content: `{"x":1}`}}}
	var g Generator = NewClient(m, "test", nil)

	if _, err := g.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	var out struct {
		X int `json:"x"`
	}
	if err := g.GenerateJSON(context.Background(), "R1", msgs("hi"), &out); err != nil {
		t.Fatal(err)
	}
	if out.X != 1 {
		t.Fatalf("X = %d", out.X)
	}
}

// ---------- Budget 边界 ----------

func TestBudgetRemainingClamp(t *testing.T) {
	b := NewBudget(10)
	b.Add(15) // 超支
	if r := b.Remaining(); r != 0 {
		t.Fatalf("超支后 Remaining 应钳到 0，实际 %d", r)
	}
	if b.Limit() != 10 {
		t.Fatalf("Limit = %d", b.Limit())
	}
}

// ---------- fitInput 边界（直接测，不经 Resilient） ----------

func TestFitInputTruncatesLongestUser(t *testing.T) {
	c := &Client{ContextTokens: 50, MaxOutputTokens: 5} // limit = (50-5)*2*0.9 = 81 runes
	long := strings.Repeat("你", 200)
	short := "short"
	msgList := []*schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage(short),
		schema.UserMessage(long),
	}
	c.fitInput(msgList)
	if len([]rune(msgList[2].Content)) >= 200 {
		t.Fatal("超长 user 消息应被截断")
	}
	if !strings.Contains(msgList[2].Content, "已截断留痕") {
		t.Fatal("截断应留痕")
	}
	if msgList[1].Content != short {
		t.Fatal("短消息不应被动")
	}
}

func TestFitInputWithinLimit(t *testing.T) {
	c := &Client{ContextTokens: 1_000_000, MaxOutputTokens: 50_000}
	original := "正常长度消息"
	msgList := []*schema.Message{schema.UserMessage(original)}
	c.fitInput(msgList)
	if msgList[0].Content != original {
		t.Fatal("窗口内消息不应被修改")
	}
}
