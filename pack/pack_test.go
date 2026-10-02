package pack

import (
	"strings"
	"testing"
	"testing/fstest"
)

// mf 构造 MapFS 文件项。
func mf(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// contractFS _shared 基线（sec/ops）+ k8s 整文件覆盖（sec）+ k8s/os-basics 同名冲突（ops）。
func contractFS() fstest.MapFS {
	return fstest.MapFS{
		"_shared/mcp/sec.yaml": mf(strings.Join([]string{
			"name: sec", "version: 1.0.0", "tools:",
			"  - {name: collect, desc: 采集}",
			"  - {name: grep, desc: 检索}",
		}, "\n")),
		"_shared/mcp/ops.yaml":   mf("name: ops\nversion: 1.0.0\ntools:\n  - {name: ping, desc: 探测}\n"),
		"k8s/mcp/sec.yaml":       mf("name: sec\nversion: 2.0.0\ntools:\n  - {name: collect, desc: 采集}\n  - {name: inspect, desc: 巡检}\n"),
		"k8s/mcp/ops.yaml":       mf("name: ops\nversion: 2.0.0\ntools:\n  - {name: ping, desc: 探测}\n"),
		"os-basics/mcp/ops.yaml": mf("name: ops\nversion: 0.9.0\ntools:\n  - {name: ping, desc: 探测}\n"),
	}
}

// TestLoadToolManifests 基线/整文件覆盖/包间同名冲突字典序生效/坏清单失败/空 FS 合法。
func TestLoadToolManifests(t *testing.T) {
	fsys := contractFS()
	fsys["k8s/mcp/broken.yaml"] = mf(":::不是 yaml")
	if _, _, err := LoadToolManifests(fsys); err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Fatalf("坏清单应报错: %v", err)
	}
	delete(fsys, "k8s/mcp/broken.yaml")
	ms, warns, err := LoadToolManifests(fsys)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// sec：k8s 整文件替换 _shared（inspect 在、grep 不在）。
	sec := ms["sec"]
	if sec == nil || sec.From != "k8s" || sec.Version != "2.0.0" || len(sec.Tools) != 2 {
		t.Fatalf("sec=%+v", sec)
	}
	if !sec.Has("collect") || !sec.Has("inspect") || sec.Has("grep") {
		t.Fatalf("整文件覆盖语义破坏: %+v", sec.Tools)
	}
	// ops：两包同名冲突 → 警告 + 字典序 k8s 生效。
	ops := ms["ops"]
	if ops == nil || ops.From != "k8s" || ops.Version != "2.0.0" {
		t.Fatalf("ops 应由字典序 k8s 生效: %+v", ops)
	}
	conflict := false
	for _, w := range warns {
		if strings.Contains(w, `tool_manifest "ops"`) && strings.Contains(w, "同名冲突") {
			conflict = true
		}
	}
	if !conflict {
		t.Fatalf("缺同名冲突警告: %v", warns)
	}
	// 无 mcp 目录 = 无清单，合法（非 nil map）。
	ms2, ws, err := LoadToolManifests(fstest.MapFS{"k8s/pack.yaml": mf("api_version: 1\n")})
	if err != nil || len(ws) != 0 || ms2 == nil || len(ms2) != 0 {
		t.Fatalf("空 FS 应合法: %v %v %+v", err, ws, ms2)
	}
}
