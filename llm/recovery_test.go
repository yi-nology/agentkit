package llm

import (
	"errors"
	"testing"
	"time"
)

func TestIsTransientStreamError(t *testing.T) {
	transient := []error{
		errors.New("stream_idle_timeout after 30s"),
		errors.New("code=model_rate_limited"),
		errors.New("MODEL_NETWORK_ERROR: connection reset"),
		errors.New("server_error: 502"),
	}
	for _, e := range transient {
		if !IsTransientStreamError(e) {
			t.Errorf("应判瞬态: %v", e)
		}
	}
	permanent := []error{
		errors.New("invalid api key"),
		errors.New("model not found: foo"),
		nil,
	}
	for _, e := range permanent {
		if IsTransientStreamError(e) {
			t.Errorf("不应判瞬态: %v", e)
		}
	}
}

func TestSuspiciousEmptyResult(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		text   int
		calls  int
		usage  int
		want   bool
	}{
		{"全空+长度截断", "length", 0, 0, 0, true},
		{"全空+未知 finish", "", 0, 0, 0, true},
		{"有文本", "length", 5, 0, 0, false},
		{"有工具调用", "tool_calls", 0, 1, 100, false},
		{"正常 stop", "stop", 0, 0, 0, false},
		{"零内容但有用量", "length", 0, 0, 500, false},
	}
	for _, tc := range cases {
		if got := SuspiciousEmptyResult(tc.finish, tc.text, tc.calls, tc.usage); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestContextExceeded(t *testing.T) {
	if !IsContextExceededFinish("context_length_exceeded", "") {
		t.Error("finishReason 形态应识别")
	}
	if !IsContextExceededFinish("", "Prompt_Too_Long") {
		t.Error("raw 形态（大小写归一）应识别")
	}
	if IsContextExceededFinish("stop", "") {
		t.Error("stop 不应误判")
	}
	if !IsContextExceededError(errors.New("this model context_window_exceeded 128000")) {
		t.Error("错误文案应识别")
	}
	if IsContextExceededError(errors.New("normal error")) {
		t.Error("普通错误不应误判")
	}
}

func TestBusyAdmission(t *testing.T) {
	for _, code := range []string{"3008", "3009", "3010", " 3010 "} {
		if !IsProviderBusyCode(code) {
			t.Errorf("%s 应判 busy", code)
		}
	}
	if IsProviderBusyCode("429") {
		t.Error("429 是限流不是 busy")
	}
	if BusyAdmissionDelay(0) != time.Second || BusyAdmissionDelay(1) != 2*time.Second {
		t.Error("两档退避 1s/2s")
	}
	if BusyAdmissionDelay(99) != 2*time.Second {
		t.Error("越界取最后一档")
	}
	if MaxStreamRecoveryRetries != 10 {
		t.Error("恢复预算常量")
	}
}
