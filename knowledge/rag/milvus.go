// Package rag Milvus 向量检索后端。
// 使用 Milvus Go SDK v2 实现 KnowledgeService 接口，
// 支持向量相似度检索（余弦相似度 / L2 距离）。
package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const (
	milvusDefaultCollection = "agentkit_knowledge"
	milvusIDField           = "id"
	milvusContentField      = "content"
	milvusFileField         = "file"
	milvusHeadingField      = "heading"
	milvusVectorField       = "embedding"
)

// MilvusConfig Milvus 连接配置。
type MilvusConfig struct {
	Address    string            // Milvus 地址，如 "localhost:19530"
	Collection string            // 集合名，默认 "agentkit_knowledge"
	Dimension  int               // 向量维度（需与 Embedder.Dim() 一致）
	MetricType entity.MetricType // 距离度量，默认 COSINE
	IndexType  string            // 索引类型，默认 "IVF_FLAT"
	NProbe     int               // 搜索时的 nprobe，默认 16
}

// MilvusStore Milvus 向量检索后端。
type MilvusStore struct {
	cfg      MilvusConfig
	client   client.Client
	embedder Embedder
}

// withDefaults 填充缺省配置（纯函数，构造器与测试共用）。
func (c MilvusConfig) withDefaults(dim int) MilvusConfig {
	if c.Collection == "" {
		c.Collection = milvusDefaultCollection
	}
	if c.Dimension == 0 {
		c.Dimension = dim
	}
	if c.MetricType == "" {
		c.MetricType = entity.COSINE
	}
	if c.IndexType == "" {
		c.IndexType = "IVF_FLAT"
	}
	if c.NProbe <= 0 { // 负数同样回退默认（透传给 SDK 会被运行期拒绝）
		c.NProbe = 16
	}
	return c
}

// NewMilvusStore 创建 Milvus 向量检索后端。
func NewMilvusStore(ctx context.Context, cfg MilvusConfig, embedder Embedder) (*MilvusStore, error) {
	cfg = cfg.withDefaults(embedder.Dim())

	c, err := client.NewClient(ctx, client.Config{Address: cfg.Address})
	if err != nil {
		return nil, fmt.Errorf("milvus: 连接失败 %s: %w", cfg.Address, err)
	}

	s := &MilvusStore{cfg: cfg, client: c, embedder: embedder}

	// 自动建表
	if err := s.ensureCollection(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}

	return s, nil
}

// ensureCollection 确保集合存在，不存在则创建。
func (s *MilvusStore) ensureCollection(ctx context.Context) error {
	has, err := s.client.HasCollection(ctx, s.cfg.Collection)
	if err != nil {
		return fmt.Errorf("milvus: 检查集合失败: %w", err)
	}
	if has {
		return nil
	}

	schema := entity.NewSchema().
		WithName(s.cfg.Collection).
		WithDescription("agentkit knowledge base").
		WithField(entity.NewField().WithName(milvusIDField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(128).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName(milvusContentField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535)).
		WithField(entity.NewField().WithName(milvusFileField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(512)).
		WithField(entity.NewField().WithName(milvusHeadingField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(512)).
		WithField(entity.NewField().WithName(milvusVectorField).WithDataType(entity.FieldTypeFloatVector).WithDim(int64(s.cfg.Dimension)))

	if err := s.client.CreateCollection(ctx, schema, entity.DefaultShardNumber); err != nil {
		return fmt.Errorf("milvus: 创建集合失败: %w", err)
	}

	// 创建向量索引
	idx, err := entity.NewIndexIvfFlat(s.cfg.MetricType, 128)
	if err != nil {
		return fmt.Errorf("milvus: 创建索引参数失败: %w", err)
	}
	if err := s.client.CreateIndex(ctx, s.cfg.Collection, milvusVectorField, idx, false); err != nil {
		return fmt.Errorf("milvus: 创建索引失败: %w", err)
	}

	// 加载集合到内存
	if err := s.client.LoadCollection(ctx, s.cfg.Collection, false); err != nil {
		return fmt.Errorf("milvus: 加载集合失败: %w", err)
	}

	return nil
}

// Index 将文档分块并索引到 Milvus。
// 幂等：先清该文件旧数据再写入——文件变短（删章节/内容精简）时 chunk 数变少，
// 只 Upsert 新块会残留 chunk_M..N-1 旧行携带过期内容参与检索挤占 topK
// （与 Local.Rescan 的全量重建同语义）。
func (s *MilvusStore) Index(ctx context.Context, file string, content string) error {
	chunks := chunkMarkdown(content)
	if err := s.DeleteFile(ctx, file); err != nil {
		return fmt.Errorf("milvus: 清理旧索引失败: %w", err)
	}
	if len(chunks) == 0 {
		return nil // 清旧后无新内容：文件已空，索引中不残留该文件数据
	}

	// 准备文本用于 embedding
	texts := make([]string, len(chunks))
	ids := make([]string, len(chunks))
	headings := make([]string, len(chunks))
	files := make([]string, len(chunks))

	for i, c := range chunks {
		texts[i] = c.content
		ids[i] = fmt.Sprintf("%s#chunk_%d", file, i)
		headings[i] = c.heading
		files[i] = file
	}

	// 批量 embedding
	vectors, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("milvus: embedding 失败: %w", err)
	}

	// 插入 Milvus
	idCol := entity.NewColumnVarChar(milvusIDField, ids)
	contentCol := entity.NewColumnVarChar(milvusContentField, texts)
	fileCol := entity.NewColumnVarChar(milvusFileField, files)
	headingCol := entity.NewColumnVarChar(milvusHeadingField, headings)
	vectorCol := entity.NewColumnFloatVector(milvusVectorField, s.cfg.Dimension, vectors)

	// Upsert 而非 Insert：主键确定性（file#chunk_N），重复 Index 同一文件时
	// 覆盖旧行——否则消费方每次重启重刷都会累积重复主键行挤占 topK 名额
	if _, err := s.client.Upsert(ctx, s.cfg.Collection, "", idCol, contentCol, fileCol, headingCol, vectorCol); err != nil {
		return fmt.Errorf("milvus: 插入失败: %w", err)
	}

	if err := s.client.Flush(ctx, s.cfg.Collection, false); err != nil {
		return fmt.Errorf("milvus: flush 失败: %w", err)
	}

	return nil
}

// DeleteFile 删除指定文件的所有索引数据。
func (s *MilvusStore) DeleteFile(ctx context.Context, file string) error {
	expr := fmt.Sprintf(`%s == "%s"`, milvusFileField, escapeMilvus(file))
	return s.client.Delete(ctx, s.cfg.Collection, "", expr)
}

// Retrieve 实现 KnowledgeService 接口：向量相似度检索。
func (s *MilvusStore) Retrieve(ctx context.Context, query string, topK int, filter Filter) ([]Chunk, error) {
	if topK <= 0 {
		topK = defaultTopK
	}

	// 查询文本 embedding
	vectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("milvus: 查询 embedding 失败: %w", err)
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("milvus: 查询 embedding 为空")
	}

	// 构建过滤表达式：key 白名单（schema 外的字段名/特殊字符会让整个检索报错，
	// 也构成表达式注入面）；value 已转义
	var expr string
	if filter != nil {
		var parts []string
		for k, v := range filter {
			if !milvusFilterKeys[k] {
				return nil, fmt.Errorf("milvus: 不支持的过滤字段 %q（可用: file, heading）", k)
			}
			parts = append(parts, fmt.Sprintf(`%s == "%s"`, k, escapeMilvus(v)))
		}
		expr = strings.Join(parts, " and ")
	}

	// 搜索参数（构造错误显式返回——吞掉后 nil sp 的报错晦涩难排查）
	sp, err := entity.NewIndexIvfFlatSearchParam(s.cfg.NProbe)
	if err != nil {
		return nil, fmt.Errorf("milvus: 搜索参数构造失败（nprobe=%d）: %w", s.cfg.NProbe, err)
	}

	// 执行搜索
	searchVectors := []entity.Vector{entity.FloatVector(vectors[0])}
	results, err := s.client.Search(ctx, s.cfg.Collection, nil, expr,
		[]string{milvusContentField, milvusFileField, milvusHeadingField},
		searchVectors, milvusVectorField, s.cfg.MetricType, topK, sp)
	if err != nil {
		return nil, fmt.Errorf("milvus: 搜索失败: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	// 解析结果
	res := results[0]
	chunks := make([]Chunk, 0, res.ResultCount)

	var contentCol *entity.ColumnVarChar
	var fileCol *entity.ColumnVarChar
	var headingCol *entity.ColumnVarChar

	for _, field := range res.Fields {
		switch field.Name() {
		case milvusContentField:
			contentCol, _ = field.(*entity.ColumnVarChar)
		case milvusFileField:
			fileCol, _ = field.(*entity.ColumnVarChar)
		case milvusHeadingField:
			headingCol, _ = field.(*entity.ColumnVarChar)
		}
	}

	scores := res.Scores
	for i := 0; i < res.ResultCount; i++ {
		content := columnValueAt(contentCol, i)
		file := columnValueAt(fileCol, i)
		heading := columnValueAt(headingCol, i)

		var score float64
		if i < len(scores) {
			score = float64(scores[i])
		}

		chunks = append(chunks, Chunk{
			Content: content,
			Score:   score,
			Metadata: map[string]string{
				"file":    file,
				"heading": heading,
			},
		})
	}

	return chunks, nil
}

// AsTool 实现 KnowledgeService 接口。
func (s *MilvusStore) AsTool() tool.BaseTool {
	return buildAsTool(s)
}

// Close 关闭 Milvus 连接。
func (s *MilvusStore) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// columnValueAt 从VarChar列中安全取值。
func columnValueAt(col *entity.ColumnVarChar, idx int) string {
	if col == nil {
		return ""
	}
	val, err := col.ValueByIdx(idx)
	if err != nil {
		return ""
	}
	return val
}

// milvusFilterKeys 检索过滤字段白名单（schema 标量字段）。
var milvusFilterKeys = map[string]bool{milvusFileField: true, milvusHeadingField: true}

// escapeMilvus 转义 Milvus 表达式中的特殊字符。
func escapeMilvus(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
