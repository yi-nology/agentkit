package acpx

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 真实 agent 冒烟测试（调用真 LLM 花钱 + 需要登录态）：
//
//	ACPX_SMOKE=1 go test ./acpx/ -run TestSmoke -v
func smokeEnabled() bool { return os.Getenv("ACPX_SMOKE") == "1" }

func TestSmokeKimiReal(t *testing.T) {
	if !smokeEnabled() {
		t.Skip("ACPX_SMOKE 未设置，跳过真实 agent 冒烟")
	}
	k := NewKimi()
	res, err := k.Run(context.Background(), RunRequest{
		Prompt:  "只回复两个字符：ok",
		Timeout: 120 * time.Second,
	})
	if err != nil {
		t.Fatalf("真实 kimi 冒烟失败: %v", err)
	}
	t.Logf("Text=%q SessionID=%q Usage=%+v", res.Text, res.SessionID, res.Usage)
	if res.Text == "" {
		t.Fatal("Text 不应为空")
	}
}

func TestSmokeMimoReal(t *testing.T) {
	if !smokeEnabled() {
		t.Skip("ACPX_SMOKE 未设置，跳过真实 agent 冒烟")
	}
	g := NewMimo()
	res, err := g.Run(context.Background(), RunRequest{
		Prompt:  "只回复两个字符：ok",
		Model:   "xiaomi/mimo-v2.5-pro", // 本机默认模型配置不被服务端支持，显式覆盖
		Timeout: 180 * time.Second,
	})
	if err != nil {
		t.Fatalf("真实 mimo 冒烟失败: %v", err)
	}
	t.Logf("Text=%q SessionID=%q Usage=%+v", res.Text, res.SessionID, res.Usage)
	if res.Text == "" {
		t.Fatal("Text 不应为空")
	}
}

func TestSmokePiReal(t *testing.T) {
	if !smokeEnabled() {
		t.Skip("ACPX_SMOKE 未设置，跳过真实 agent 冒烟")
	}
	p := NewPi()
	res, err := p.Run(context.Background(), RunRequest{
		Prompt:  "只回复两个字符：ok",
		Timeout: 180 * time.Second,
	})
	if err != nil {
		t.Fatalf("真实 pi 冒烟失败: %v", err)
	}
	t.Logf("Text=%q Usage=%+v", res.Text, res.Usage)
	if res.Text == "" {
		t.Fatal("Text 不应为空")
	}
}

func TestSmokeDshReal(t *testing.T) {
	if !smokeEnabled() {
		t.Skip("ACPX_SMOKE 未设置，跳过真实 agent 冒烟")
	}
	d := NewDsh()
	res, err := d.Run(context.Background(), RunRequest{
		Prompt:  "只回复两个字符：ok",
		Timeout: 180 * time.Second,
	})
	if err != nil {
		t.Fatalf("真实 dsh 冒烟失败: %v", err)
	}
	t.Logf("Text=%q", res.Text)
	if res.Text == "" {
		t.Fatal("Text 不应为空")
	}
}

func TestSmokeMcodeReal(t *testing.T) {
	if !smokeEnabled() {
		t.Skip("ACPX_SMOKE 未设置，跳过真实 agent 冒烟")
	}
	m := NewMcode()
	res, err := m.Run(context.Background(), RunRequest{
		Prompt:  "只回复两个字符：ok", // 不传 Model:用 CLI 缺省(实测 MiniMax-M3.1-Flash-Preview)
		Timeout: 180 * time.Second,
	})
	if err != nil {
		t.Fatalf("真实 mcode 冒烟失败: %v", err)
	}
	t.Logf("Text=%q SessionID=%q Usage=%+v", res.Text, res.SessionID, res.Usage)
	if res.Text == "" {
		t.Fatal("Text 不应为空")
	}
	if res.SessionID == "" {
		t.Fatal("SessionID 不应为空(exec.result 信封必有 sessionId,丢失=解析退化)")
	}
	if res.Usage.InputTokens == 0 {
		t.Fatal("Usage.InputTokens 不应为 0(信封 usage 解析失效)")
	}

	// 续聊实弹:--session 续跑,验证记忆与 sessionId 稳定
	res2, err := m.Run(context.Background(), RunRequest{
		Prompt:    "你刚才回复的两个字符是什么?原样只回复那两个字符",
		SessionID: res.SessionID,
		Timeout:   180 * time.Second,
	})
	if err != nil {
		t.Fatalf("真实 mcode 续聊失败: %v", err)
	}
	t.Logf("续聊 Text=%q SessionID=%q", res2.Text, res2.SessionID)
	if !strings.Contains(res2.Text, "ok") {
		t.Fatalf("续聊应记得首轮回复, got %q", res2.Text)
	}
}
