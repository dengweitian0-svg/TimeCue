package app

import (
	"strconv"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// sweepResult 保存一条发生及其父日程版本，避免扫线过程暴露领域对象以外的查询状态。
type sweepResult struct {
	occurrence domain.Occurrence
	revision   int64
}

// sweepOverlaps 按半开区间判断两个发生是否相交，边界相接不算重叠。
func sweepOverlaps(left, right domain.Occurrence) bool {
	return left.StartLocal.Unix() < right.EndLocal.Unix() && right.StartLocal.Unix() < left.EndLocal.Unix()
}

// sweepResultKey 返回发生身份键，用于稳定保存重叠标记。
func sweepResultKey(item sweepResult) string {
	return strconv.FormatInt(item.occurrence.StartLocal.Unix(), 10) + "\x00" +
		strconv.FormatInt(int64(item.occurrence.ScheduleID), 10) + "\x00" + item.occurrence.OriginalDate
}

// occurrenceSweep 在稳定发生流上维护活动区间，并延迟页面候选直到其重叠状态确定。
//
// 查询游标只影响返回候选，游标之前的发生仍保留在活动集合中，以便跨分页正确标记
// 页面首条发生。扫描在页面候选全部定论后停止，不执行无界的未来全量展开。
type occurrenceSweep struct {
	next   func() (sweepResult, bool, error)
	active []sweepResult
}

// newOccurrenceSweep 创建一个按稳定顺序读取发生的扫线器。
func newOccurrenceSweep(next func() (sweepResult, bool, error)) *occurrenceSweep {
	return &occurrenceSweep{next: next}
}

// collect 收集一页候选，并返回候选的最终重叠标记及是否存在后续结果。
func (s *occurrenceSweep) collect(limit int, after func(sweepResult) bool) ([]sweepResult, map[string]bool, bool, error) {
	candidates := make([]sweepResult, 0, limit)
	marked := make(map[string]bool)
	hasMore := false
	for {
		item, ok, err := s.next()
		if err != nil {
			return nil, nil, false, err
		}
		if !ok {
			return candidates, marked, hasMore, nil
		}
		s.observe(item, marked)
		if after(item) {
			if len(candidates) < limit {
				candidates = append(candidates, item)
			} else {
				// 已经收满当前页后仍读到一条游标之后的发生，说明
				// 即使扫线提前结束也必须生成下一页游标。
				hasMore = true
			}
		}
		if len(candidates) != limit {
			continue
		}

		// 候选中最长的区间仍可能被后续开始的发生命中；开始时间达到其
		// 结束时间后，所有候选的标记都已稳定，可以停止读取后续未来数据。
		maxEnd := candidates[0].occurrence.EndLocal.Unix()
		for _, candidate := range candidates[1:] {
			if end := candidate.occurrence.EndLocal.Unix(); end > maxEnd {
				maxEnd = end
			}
		}
		if item.occurrence.StartLocal.Unix() >= maxEnd {
			return candidates, marked, hasMore, nil
		}
	}
}

// observe 删除已结束活动区间，并标记当前发生与所有活动区间的相交关系。
func (s *occurrenceSweep) observe(item sweepResult, marked map[string]bool) {
	start := item.occurrence.StartLocal.Unix()
	active := s.active[:0]
	for _, existing := range s.active {
		if existing.occurrence.EndLocal.Unix() > start {
			active = append(active, existing)
		}
	}
	s.active = active
	for _, existing := range s.active {
		if !sweepOverlaps(existing.occurrence, item.occurrence) {
			continue
		}
		marked[sweepResultKey(existing)] = true
		marked[sweepResultKey(item)] = true
	}
	s.active = append(s.active, item)
}
