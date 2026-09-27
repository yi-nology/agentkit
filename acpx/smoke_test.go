package acpx

import (
	"context"
	"os"
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
