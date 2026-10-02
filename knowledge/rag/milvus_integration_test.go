package rag

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

// hashEmbedder 确定性测试 embedding：文本 hash → 固定维度向量。
// 相似文本（共享字符）向量相近，足以验证相似度检索链路。
type hashEmbedder struct{ dim int }

func (h *hashEmbedder) Dim() int { return h.dim }

func (h *hashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		vec := make([]float32, h.dim)
		for j, ch := range t {
			vec[j%h.dim] += float32(int(ch)%251) / 251.0
		}
		out[i] = vec
	}
	return out, nil
}

// testMilvusAddr 集成测试环境开关：
//
//	MILVUS_TEST_ADDR=localhost:19530 go test ./knowledge/rag/ -run TestMilvusIntegration
//
// 未设置时 Skip（单元测试不依赖外部服务）。
func testMilvusAddr() string { return os.Getenv("MILVUS_TEST_ADDR") }

func TestMilvusIntegration(t *testing.T) {
	addr := testMilvusAddr()
	if addr == "" {
		t.Skip("MILVUS_TEST_ADDR 未设置，跳过 Milvus 集成测试（启动: docker compose -f docker-compose.milvus-test.yml up -d）")
	}

	collection := fmt.Sprintf("agentkit_it_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	store, err := NewMilvusStore(ctx, MilvusConfig{
		Address:    addr,
		Collection: collection,
		Dimension:  16,
		MetricType: entity.COSINE,
	}, &hashEmbedder{dim: 16})
	if err != nil {
		t.Fatalf("建连/建表失败: %v", err)
	}
	defer func() {
		_ = store.client.DropCollection(ctx, collection)
		store.Close()
	}()

	// 1. Index：写入 3 篇文档（每篇内部按标题分块）
	docs := map[string]string{
		"go-standards.md": "# Go 规范\n\n错误必须显式处理。\n\n## 并发\n\n共享状态加锁。",
		"deploy.md":       "# 部署\n\nArgoCD GitOps 流水线发布。\n\n## Nacos\n\n配置中心统一下发。",
		"security.md":     "# 安全\n\n禁止硬编码凭证，密钥走环境变量。",
	}
	for name, content := range docs {
		if err := store.Index(ctx, name, content); err != nil {
			t.Fatalf("索引 %s 失败: %v", name, err)
		}
	}
	// flush 后需要短暂时间可检索（standalone local storage 通常即时）
	time.Sleep(2 * time.Second)

	// 2. Retrieve：向量相似度命中
	chunks, err := store.Retrieve(ctx, "ArgoCD 部署", 3, nil)
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("应检索到结果")
	}
	found := false
	for _, c := range chunks {
		if c.Metadata["file"] == "deploy.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("top 结果应含 deploy.md，得到 %v", chunks)
	}

	// 3. Filter：按 file 精确过滤
	filtered, err := store.Retrieve(ctx, "配置", 5, Filter{"file": "deploy.md"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range filtered {
		if c.Metadata["file"] != "deploy.md" {
			t.Fatalf("过滤失效: %v", c.Metadata)
		}
	}

	// 4. DeleteFile：删除后不再命中
	if err := store.DeleteFile(ctx, "security.md"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	time.Sleep(2 * time.Second)

	// 5. AsTool
	if store.AsTool() == nil {
		t.Fatal("AsTool 应非 nil")
	}
}
