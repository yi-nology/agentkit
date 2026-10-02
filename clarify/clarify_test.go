package clarify

import (
	"testing"
	"testing/fstest"
)

func stubOptions() []string {
	return []string{"CPU", "内存", "磁盘 IO"}
}

func TestOrdinalIndex(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"第一个", 1, true},
		{"第1个", 1, true},
		{"第 2 个", 2, true},
		{"2", 2, true},
		{"二", 2, true},
		{"选项三", 3, true},
		{"选一", 1, true},
		{"1.", 1, true},
		{"十", 0, false},      // 超出汉字数字表
		{"第五个", 0, false},    // 越界由调用方按 len(options) 判，这里解析本身成立=5？
		{"1个进程在跑", 0, false}, // 普通句子不误伤（剥离后非单字）
		{"看看日志吧", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		n, ok := OrdinalIndex(tc.in)
		// 「第五个」解析为 5，越界与否由调用方对 options 长度裁决
		if tc.in == "第五个" {
			if n != 5 || !ok {
				t.Errorf("OrdinalIndex(%q) = (%d,%v), 期望 (5,true)", tc.in, n, ok)
			}
			continue
		}
		if ok != tc.ok || (ok && n != tc.want) {
			t.Errorf("OrdinalIndex(%q) = (%d,%v), 期望 (%d,%v)", tc.in, n, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveAnswer(t *testing.T) {
	opts := stubOptions()
	cases := []struct {
		answer         string
		picked         string
		fallback, want bool
	}{
		{"第一个", "CPU", false, true},
		{"第2个", "内存", false, true},
		{"选项三", "磁盘 IO", false, true},
		{"内存", "内存", false, true},
		{"帮我看看磁盘io", "磁盘 IO", false, true}, // 包含匹配容大小写/空格
		{"不清楚，全面查", "", true, true},
		{"不清楚", "", true, true}, // 逃生选项前缀命中
		{"看看日志吧", "", false, false},
		{"第五个", "", false, false}, // 序数越界=放弃
		{"", "", false, false},
	}
	for _, c := range cases {
		picked, fb, ok := ResolveAnswer(opts, "不清楚，全面查", c.answer)
		if ok != c.want || (ok && (picked != c.picked || fb != c.fallback)) {
			t.Fatalf("ResolveAnswer(%q) = (%q,%v,%v) want (%q,%v,%v)", c.answer, picked, fb, ok, c.picked, c.fallback, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := []Entry{
		{Word: "负载", Dimension: "system_load", Vague: true, Clarify: &Options{Question: "哪一类？", Options: stubOptions(), Fallback: "不清楚，全面查"}},
		{Word: "磁盘满", Dimension: "disk_space"},
	}
	if err := Validate(ok); err != nil {
		t.Fatalf("合法词表不应报错: %v", err)
	}
	cases := []struct {
		name string
		in   []Entry
	}{
		{"缺 word", []Entry{{Dimension: "cpu"}}},
		{"词重复", []Entry{{Word: "负载", Dimension: "system_load"}, {Word: "负载", Dimension: "cpu"}}},
		{"维度键非法", []Entry{{Word: "负载", Dimension: "System-Load"}}},
		{"vague 缺 clarify", []Entry{{Word: "负载", Dimension: "system_load", Vague: true}}},
		{"vague clarify 缺 fallback", []Entry{{Word: "负载", Dimension: "system_load", Vague: true,
			Clarify: &Options{Question: "哪一类？", Options: stubOptions()}}}},
		{"非 vague 配 clarify", []Entry{{Word: "磁盘满", Dimension: "disk_space",
			Clarify: &Options{Question: "？", Options: stubOptions(), Fallback: "全查"}}}},
	}
	for _, tc := range cases {
		if err := Validate(tc.in); err == nil {
			t.Errorf("%s: 应报错", tc.name)
		}
	}
}

func TestLoadVocab(t *testing.T) {
	t.Run("缺文件返回 nil", func(t *testing.T) {
		entries, err := LoadVocab(fstest.MapFS{}, "_shared/term_map.yaml")
		if err != nil || entries != nil {
			t.Fatalf("缺文件应 (nil,nil): (%v,%v)", entries, err)
		}
	})
	t.Run("解析失败 fail-fast", func(t *testing.T) {
		fsys := fstest.MapFS{"_shared/term_map.yaml": &fstest.MapFile{Data: []byte("term_map: [broken")}}
		if _, err := LoadVocab(fsys, "_shared/term_map.yaml"); err == nil {
			t.Fatal("解析失败应返回 error")
		}
	})
	t.Run("正常加载", func(t *testing.T) {
		fsys := fstest.MapFS{"_shared/term_map.yaml": &fstest.MapFile{Data: []byte(
			"term_map:\n  - word: 负载\n    dimension: system_load\n    terms: [系统负载, load]\n    vague: true\n    clarify:\n      question: 哪一类？\n      options: [CPU, 内存]\n      fallback: 不清楚，全面查\n")}}
		entries, err := LoadVocab(fsys, "_shared/term_map.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Word != "负载" || entries[0].Dimension != "system_load" ||
			!entries[0].Vague || entries[0].Clarify == nil || len(entries[0].Clarify.Options) != 2 {
			t.Fatalf("加载结果不符: %+v", entries)
		}
		if err := Validate(entries); err != nil {
			t.Fatalf("加载结果应过内在校验: %v", err)
		}
	})
}
