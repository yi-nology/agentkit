// Langfuse 官方 OpenAPI 契约类型（依据 cloud.langfuse.com/generated/api/openapi.yml；
// 沉淀自 heimdallr internal/observe）。只建模读取路径用到的字段。
package langfuse

import (
	"encoding/json"
	"time"
)

// Trace Langfuse trace（GET /api/public/traces 与 GET /api/public/traces/{id} 的并集形态）。
type Trace struct {
	ID           string          `json:"id"`
	Timestamp    time.Time       `json:"timestamp"`
	Name         string          `json:"name"`
	SessionID    string          `json:"sessionId,omitempty"`
	UserID       string          `json:"userId,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	Latency      *float64        `json:"latency,omitempty"`   // 秒
	TotalCost    *float64        `json:"totalCost,omitempty"` // USD
	Observations []Observation   `json:"observations,omitempty"`
}

// Observation Langfuse observation（SPAN | GENERATION | EVENT | TOOL）。
type Observation struct {
	ID            string          `json:"id"`
	TraceID       string          `json:"traceId,omitempty"`
	Type          string          `json:"type"`
	Name          string          `json:"name,omitempty"`
	StartTime     time.Time       `json:"startTime"`
	EndTime       time.Time       `json:"endTime"` // 零值 = null（仍在运行或缺失）
	Model         string          `json:"model,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	Level         string          `json:"level,omitempty"`         // DEBUG | DEFAULT | WARNING | ERROR
	StatusMessage string          `json:"statusMessage,omitempty"` // level 为 WARNING/ERROR 时的人类可读原因
	// 优先 usageDetails/costDetails（新口径），fallback usage（旧口径）。
	UsageDetails map[string]int     `json:"usageDetails,omitempty"`
	CostDetails  map[string]float64 `json:"costDetails,omitempty"`
	Usage        struct {
		Input     int     `json:"input"`
		Output    int     `json:"output"`
		Total     int     `json:"total"`
		TotalCost float64 `json:"totalCost"`
	} `json:"usage"`
	ParentObservationID string `json:"parentObservationId,omitempty"`
}

// UsageTokens usage 口径：新口径 usageDetails 优先，旧口径 usage 兜底。
func (o Observation) UsageTokens() (input, output, total int) {
	if o.UsageDetails != nil {
		return o.UsageDetails["input"], o.UsageDetails["output"], o.UsageDetails["total"]
	}
	return o.Usage.Input, o.Usage.Output, o.Usage.Total
}

// UsageCost cost 口径：costDetails.total 优先，usage.totalCost 兜底。
func (o Observation) UsageCost() float64 {
	if o.CostDetails != nil {
		return o.CostDetails["total"]
	}
	return o.Usage.TotalCost
}
