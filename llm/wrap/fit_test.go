package wrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/yi-nology/agentkit/textutil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// assistantRound 构造一个带工具调用的 assistant 轮起点（工具结果的所属轮）。
func assistantRound(i int) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
		{ID: fmt.Sprintf("c%d", i), Function: schema.FunctionCall{Name: "t"}},
	}}
}

func bigTool(role schema.RoleType, n int) *schema.Message {
	return &schema.Message{Role: role, Content: strings.Repeat("x", n)}
}

func TestFitMessagesPassthrough(t *testing.T) {
	msgs := []*schema.Message{
		bigTool(schema.System, 100),
		bigTool(schema.User, 100),
	}
	out, res := FitMessages(msgs, 10_000)
	same := len(out) == len(msgs) && out[0] == msgs[0] && out[1] == msgs[1]
	if res.Action != FitPass || !same {
		t.Fatalf("未超限应零拷贝直通，got action=%s same=%v", res.Action, same)
	}
}

func TestFitMessagesMicrocompact(t *testing.T) {
	// 8 个工具结果轮：清更早 3 轮（保最近 5 轮）即可入窗（v3.44.0 组粒度）
	msgs := []*schema.Message{bigTool(schema.System, 10), bigTool(schema.User, 100)}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, assistantRound(i), bigTool(schema.Tool, 2000))
	}
	limit := 10 + 100 + 5*2000 + 1000 // 清 3 轮后留余量
	out, res := FitMessages(msgs, limit)
	if res.Action != FitMicro {
		t.Fatalf("期望 FitMicro，got %s（before=%d limit=%d）", res.Action, res.Before, res.Limit)
	}
	if res.Cleared != 3 {
		t.Fatalf("期望清理 3 条，got %d", res.Cleared)
	}
	if len(out) != len(msgs) {
		t.Fatal("消息条数必须不变（语法安全）")
	}
	// 工具结果下标：3,5,7,9,11,13,15,17（assistant 在 2,4,6,8,10,12,14,16）
	for i := 0; i < 5; i++ {
		if got := out[9+2*i].Content; got != strings.Repeat("x", 2000) {
			t.Fatalf("最近 5 轮不应被清理，第 %d 轮 len=%d", i, len(got))
		}
	}
	for i := 0; i < 3; i++ {
		if out[3+2*i] == msgs[3+2*i] {
			t.Fatal("被清理消息应为克隆，不得污染调用方")
		}
		if got := out[3+2*i].Content; got != toolPlaceholder {
			t.Fatalf("旧工具结果应为占位符，got %q", got)
		}
	}
	if msgs[3].Content == toolPlaceholder {
		t.Fatal("不得改写调用方原消息")
	}
}

func TestFitMessagesProtectsErrorAndReminderResults(t *testing.T) {
	// 错误/提醒结果保护（compact「出错结果默认保留」同款）：10 轮淘汰 5 轮，
	// 其中第 0 轮带 tool-error 标记、第 1 轮带提醒信封——受保护不清理，
	// 只清第 2/3/4 轮
	msgs := []*schema.Message{bigTool(schema.User, 100)}
	errContent := "调用失败：【tool-error】deadline exceeded" + strings.Repeat("x", 2000)
	reminderContent := "说明 <system-reminder>NOT USER INPUT</system-reminder>" + strings.Repeat("y", 2000)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, assistantRound(i))
		switch i {
		case 0:
			msgs = append(msgs, &schema.Message{Role: schema.Tool, ToolCallID: "c0", Content: errContent})
		case 1:
			msgs = append(msgs, &schema.Message{Role: schema.Tool, ToolCallID: "c1", Content: reminderContent})
		default:
			msgs = append(msgs, bigTool(schema.Tool, 2000))
		}
	}
	out, res := FitMessages(msgs, 12_000)
	if res.Cleared != 3 {
		t.Fatalf("保护项不清理应只清 3 条，got %d（action=%s）", res.Cleared, res.Action)
	}
	if out[2].Content != errContent {
		t.Fatal("tool-error 结果应受保护不清理")
	}
	if out[4].Content != reminderContent {
		t.Fatal("提醒信封结果应受保护不清理")
	}
}

func TestFitMessagesMicrocompactBelowThresholdAbandoned(t *testing.T) {
	// 唯一旧工具结果太小（清理量 < minClearSavings）：L1 放弃，仅 L2 截 user
	msgs := []*schema.Message{bigTool(schema.User, 6000), bigTool(schema.Tool, 400)}
	out, res := FitMessages(msgs, 4600)
	if res.Action != FitTruncated {
		t.Fatalf("L1 不足应落 L2 截断，got %s", res.Action)
	}
	if res.Cleared != 0 {
		t.Fatalf("L1 放弃则 Cleared 应为 0，got %d", res.Cleared)
	}
	if out[1].Content != msgs[1].Content {
		t.Fatal("L1 放弃且未触发 L3 时工具结果不应被改")
	}
	if len([]rune(out[0].Content)) >= 6000 {
		t.Fatal("user 消息应被截断变短")
	}
}

func TestFitMessagesTruncateLongestUser(t *testing.T) {
	msgs := []*schema.Message{
		bigTool(schema.System, 50),
		bigTool(schema.User, 8000),
		bigTool(schema.Assistant, 50),
	}
	out, res := FitMessages(msgs, 4000)
	if res.Action != FitTruncated {
		t.Fatalf("期望 FitTruncated，got %s", res.Action)
	}
	if len([]rune(out[1].Content)) >= 8000 {
		t.Fatal("user 消息应被截断")
	}
	if !strings.Contains(out[1].Content, textutil.FitInputNote) {
		t.Fatal("截断应带留痕注记")
	}
	if !strings.HasPrefix(out[1].Content, "\n") {
		t.Fatal("截断格式应与 agentkit fitInput 一致（前导换行）")
	}
}

func TestFitMessagesLevel3ToolTruncation(t *testing.T) {
	// user 很小、工具结果巨大且多（6 轮）：L1 清 1 轮不够、L2 截 user 也不够 → L3 迭代
	msgs := []*schema.Message{bigTool(schema.User, 100)}
	for i := 0; i < 6; i++ {
		msgs = append(msgs, assistantRound(i), bigTool(schema.Tool, 3000))
	}
	out, res := FitMessages(msgs, 6000)
	if res.After > res.Limit {
		t.Fatalf("L3 后应入窗，after=%d limit=%d", res.After, res.Limit)
	}
	if res.Action != FitMicroTrunc {
		t.Fatalf("期望 FitMicroTrunc，got %s", res.Action)
	}
	if len(out) != len(msgs) {
		t.Fatal("消息条数必须不变")
	}
}

// fakeChatModel 直通桩（记录最后输入）。
type fakeChatModel struct {
	mu     sync.Mutex
	lastIn []*schema.Message
}

func TestCalibrationInstanceScoped(t *testing.T) {
	// v0.12.0：校准槽实例内化——响应直采配对，脏样本界内不采信，实例间不串槽
	f := NewFitModel(&fakeChatModel{}, "s1", "m1", 10_000, 0, nil)
	f.calibRecord(800, 1000)
	if got := f.calibPerChar(); got < 0.79 || got > 0.81 {
		t.Fatalf("系数应为 0.8，got %f", got)
	}
	f.calibRecord(9000, 1000) // 系数 9 > 4：脏样本
	if got := f.calibPerChar(); got != 0 {
		t.Fatalf("脏样本应返回 0，got %f", got)
	}
	f2 := NewFitModel(&fakeChatModel{}, "s2", "m2", 10_000, 0, nil)
	f.calibRecord(500, 1000)
	if got := f2.calibPerChar(); got != 0 {
		t.Fatalf("实例隔离：f2 应无样本，got %f", got)
	}
}

func (f *fakeChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	f.lastIn = input
	f.mu.Unlock()
	return bigTool(schema.Assistant, 5), nil
}

func (f *fakeChatModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.mu.Lock()
	f.lastIn = input
	f.mu.Unlock()
	return nil, errors.New("fake: stream 未实现")
}

func (f *fakeChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

func TestFitModelGenerateFits(t *testing.T) {
	inner := &fakeChatModel{}
	var events []FitResult
	fm := NewFitModel(inner, "R3", "m", 10_000, 0, func(stage string, res FitResult) {
		events = append(events, res)
	})
	msgs := []*schema.Message{bigTool(schema.User, 30_000)}
	out, err := fm.Generate(context.Background(), msgs)
	if err != nil || out == nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	inner.mu.Lock()
	sent := inner.lastIn
	inner.mu.Unlock()
	if len([]rune(sent[0].Content)) >= 30_000 {
		t.Fatal("发送前应完成拟合截断")
	}
	if len(events) != 1 || events[0].Action != FitTruncated {
		t.Fatalf("拟合事件应上报 OnFit，got %v", events)
	}
}

func TestFitModelPassthroughZeroWindow(t *testing.T) {
	inner := &fakeChatModel{}
	fm := NewFitModel(inner, "qa", "m", 0, 0, nil)
	msgs := []*schema.Message{bigTool(schema.User, 100_000)}
	if _, err := fm.Generate(context.Background(), msgs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	inner.mu.Lock()
	lastIn := inner.lastIn
	inner.mu.Unlock()
	if len([]rune(lastIn[0].Content)) != 100_000 {
		t.Fatal("窗口 ≤0 应完全直通")
	}
}

func TestFitModelWithToolsRewrap(t *testing.T) {
	inner := &fakeChatModel{}
	fm := NewFitModel(inner, "R3", "m", 10_000, 0, nil)
	bound, err := fm.WithTools([]*schema.ToolInfo{})
	if err != nil {
		t.Fatalf("WithTools 失败: %v", err)
	}
	bfm, ok := bound.(*FitModel)
	if !ok {
		t.Fatalf("绑定产物应仍是 FitModel，got %T", bound)
	}
	if bfm.BaseChatModel != model.BaseChatModel(inner) {
		t.Fatal("绑定产物内层应是 WithTools 返回的模型（拟合不因绑定失效）")
	}
}

// 时序不变量（v3.37.0 测试卡点）：拟合只允许改 Tool/User 消息的 Content——
// 消息条数、role 序列、tool_call/tool_result 配对字段必须逐字节保持。
func TestFitMessagesPreservesGrammar(t *testing.T) {
	msgs := []*schema.Message{
		bigTool(schema.System, 50),
		bigTool(schema.User, 500),
		{Role: schema.Assistant, Content: "查看", ToolCalls: []schema.ToolCall{
			{ID: "call1", Function: schema.FunctionCall{Name: "get_file", Arguments: `{"path":"a.go"}`}},
		}},
		bigTool(schema.Tool, 3000),
		{Role: schema.Assistant, Content: "再看"},
		bigTool(schema.Tool, 3000),
	}
	for i := range msgs[3].Content {
		_ = i
	}
	msgs[3].ToolCallID = "call1"
	msgs[5].ToolCallID = "call2"

	out, _ := FitMessages(msgs, 2000)
	if len(out) != len(msgs) {
		t.Fatal("消息条数必须不变")
	}
	for i := range msgs {
		if out[i].Role != msgs[i].Role {
			t.Fatalf("role 序列必须不变，第 %d 条 %s→%s", i, msgs[i].Role, out[i].Role)
		}
		if len(out[i].ToolCalls) != len(msgs[i].ToolCalls) {
			t.Fatalf("ToolCalls 必须不变，第 %d 条", i)
		}
		if out[i].ToolCallID != msgs[i].ToolCallID {
			t.Fatalf("ToolCallID 必须不变，第 %d 条", i)
		}
		if msgs[i].Role != schema.Tool && msgs[i].Role != schema.User && out[i].Content != msgs[i].Content {
			t.Fatalf("非 Tool/User 消息的 Content 不得改动，第 %d 条 role=%s", i, msgs[i].Role)
		}
	}
}

func TestFitModelOnBreakdown(t *testing.T) {
	inner := &fakeChatModel{}
	var got map[string]int
	var gotStage string
	fm := NewFitModel(inner, "chat", "m", 10_000, 0, nil)
	fm.OnBreakdown = func(stage string, byRole map[string]int) {
		gotStage = stage
		got = byRole
	}
	if _, err := fm.Generate(context.Background(), []*schema.Message{
		bigTool(schema.System, 50), bigTool(schema.User, 30_000),
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 实发构成（拟合后）——×3/2 CJK 口径（v0.12.2）下 limit≈12825，
	// user 30000 截到 ~12763；桶值为截断后实发量而非拟合前总量
	if gotStage != "chat" || got["system"] != 50 || got["user"] >= 30_000 || got["user"] < 12_000 {
		t.Fatalf("分解回调应按 role 分桶实发 rune 量，got stage=%s bucket=%v", gotStage, got)
	}
	// nil 回调直通不 panic
	fm2 := NewFitModel(inner, "qa", "m", 10_000, 0, nil)
	if _, err := fm2.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 30_000)}); err != nil {
		t.Fatalf("nil OnBreakdown 应直通: %v", err)
	}
}

// TestFitMessagesLevel3NoDeadloopOnSmallResults 回归（第八轮审计 C 级）：
// 全部可截工具结果都 ≤ 占位符长度时，L3 不得选中「置换后不缩反涨」的候选
// ——此前零进展死循环挂死 100% CPU。数百轮小结果（"OK"级）+ 长正文触发。
func TestFitMessagesLevel3NoDeadloopOnSmallResults(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, schema.UserMessage(strings.Repeat("问", 4000)))
	for i := 0; i < 300; i++ {
		msgs = append(msgs,
			&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "c", Type: "function", Function: schema.FunctionCall{Name: "x", Arguments: "{}"}}}},
			&schema.Message{Role: schema.Tool, ToolCallID: "c", Content: "OK"},
		)
	}
	done := make(chan struct{})
	var out []*schema.Message
	go func() {
		out, _ = FitMessages(msgs, 2000)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("L3 死循环：小结果场景 5s 未返回")
	}
	if total := runeTotal(out); total > 2600 { // 截最长 user 后应入窗或无可裁直通
		for _, m := range out {
			if m.Role == schema.Tool && !strings.Contains(m.Content, "OK") && !strings.Contains(m.Content, "截断") && m.Content != toolPlaceholder {
				t.Fatalf("小结果不应被改写: %q", m.Content)
			}
		}
		_ = total
	}
}

// TestFitModelGenerateCalibratesFromResponse 回归（第八轮审计 I）：
// Generate 响应直采——usage 与实发 rune 数（After 口径）同调用配对。
func TestFitModelGenerateCalibratesFromResponse(t *testing.T) {
	base := &usageEchoModel{}
	f := NewFitModel(base, "cal", "m", 1000, 0, nil)
	msgs := []*schema.Message{schema.UserMessage(strings.Repeat("字", 3000))}
	if _, err := f.Generate(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	sent := runeTotal(base.lastInLocked())
	f.calibMu.Lock()
	u, c := f.calibUsage, f.calibChars
	f.calibMu.Unlock()
	if u != 421 || c != sent {
		t.Fatalf("直采配对应为 (usage=421, sent=%d)，got (%d,%d)", sent, u, c)
	}
}

// usageEchoModel 回传固定 PromptTokens 的桩（直采配对断言用）。
type usageEchoModel struct {
	mu     sync.Mutex
	lastIn []*schema.Message
}

func (m *usageEchoModel) lastInLocked() []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastIn
}

func (m *usageEchoModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.lastIn = input
	m.mu.Unlock()
	return &schema.Message{
		Role:    schema.Assistant,
		Content: "ok",
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{PromptTokens: 421, CompletionTokens: 1},
		},
	}, nil
}

func (m *usageEchoModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, w := schema.Pipe[*schema.Message](1)
	w.Send(out, nil)
	w.Close()
	return sr, nil
}
