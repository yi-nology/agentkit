package blackboard

import (
	"context"
	"fmt"
)

// Specialist 专家：观察黑板增量，判断与己相关则贡献条目。
type Specialist struct {
	// Name 专家名（贡献条目的 Author）。
	Name string
	// Act 观察与贡献。since = 该专家上一轮读到的最大 Seq（首轮 0）；
	// 返回 contributed=false 表示本轮无事可做。
	Act func(ctx context.Context, b *Board, since int64) (contributed bool, err error)
}

// ConveneOptions 召集配置。
type ConveneOptions struct {
	// MaxRounds 轮次上限（默认 3；每轮所有专家依次观察一次）。
	MaxRounds int
}

// ConveneResult 召集结果。
type ConveneResult struct {
	Rounds     int   // 实际轮数
	Total      int   // 黑板条目总数
	CurrentMax int64 // 当前最大 Seq（供调用方留痕）
}

// Convene 召集专家轮转协作：每轮按序让每位专家观察增量并可写入；
// 一整轮无人贡献（共识达成）或达 MaxRounds 停止。
// Specialist.Name 须非空且全局唯一（构建期 fail-fast）：观察游标按 Name 键控，
// 重名会共享游标（后执行者读到被同名者推进的 since，跳过的增量永不重看）。
func Convene(ctx context.Context, b *Board, specialists []Specialist, opts *ConveneOptions) (*ConveneResult, error) {
	if b == nil {
		return nil, fmt.Errorf("blackboard: Board 不能为空")
	}
	if len(specialists) == 0 {
		return nil, fmt.Errorf("blackboard: specialists 不能为空")
	}
	seen := make(map[string]bool, len(specialists))
	for _, sp := range specialists {
		if sp.Name == "" {
			return nil, fmt.Errorf("blackboard: 专家名不能为空")
		}
		if seen[sp.Name] {
			return nil, fmt.Errorf("blackboard: 专家名重复: %s（观察游标按名键控，重名会互吞增量）", sp.Name)
		}
		seen[sp.Name] = true
	}
	maxRounds := 3
	if opts != nil && opts.MaxRounds > 0 {
		maxRounds = opts.MaxRounds
	}

	// 每位专家的观察游标
	cursor := make(map[string]int64, len(specialists))
	rounds := 0
	for r := 0; r < maxRounds; r++ {
		rounds++
		contributed := false
		for _, sp := range specialists {
			if sp.Act == nil {
				continue
			}
			did, err := sp.Act(ctx, b, cursor[sp.Name])
			if err != nil {
				return nil, fmt.Errorf("blackboard: 专家 %s 第 %d 轮失败: %w", sp.Name, r+1, err)
			}
			// 无论是否贡献，游标推进到当前最大值（错过的不重看——每轮都是新机会）
			if max := b.maxSeq(); max > cursor[sp.Name] {
				cursor[sp.Name] = max
			}
			if did {
				contributed = true
			}
		}
		if !contributed {
			break // 共识达成：一整轮无人补充
		}
	}
	entries := b.Entries()
	return &ConveneResult{
		Rounds:     rounds,
		Total:      len(entries),
		CurrentMax: b.maxSeq(),
	}, nil
}

func (b *Board) maxSeq() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}
