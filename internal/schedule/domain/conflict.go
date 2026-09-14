package domain

import (
	"sort"
	"time"
)

// ConflictStatus 表示一次冲突检查是否真的比较了发生，以及没有比较的原因。
type ConflictStatus string

const (
	// ConflictChecked 表示已经比较过目标批次和全部有效发生。
	ConflictChecked ConflictStatus = "CHECKED"
	// ConflictNoTarget 表示操作后严格晚于 now 不再有可检查的发生。
	ConflictNoTarget ConflictStatus = "NO_FUTURE_OCCURRENCE"
	// ConflictPastTarget 表示要求检查的单次发生已经在过去，不能改查其他发生。
	ConflictPastTarget ConflictStatus = "PAST_OCCURRENCE"
	// ConflictClosedGroup 表示父日程关闭，恢复前的快照不产生任何发生。
	ConflictClosedGroup ConflictStatus = "SCHEDULE_CLOSED"
)

// ConflictCheck 表示一次冲突检查的目标、明确状态和命中的冲突集合。
type ConflictCheck struct {
	Status           ConflictStatus
	TargetUnix       int64
	Targets          []ConflictTarget
	Conflicts        []OccurrenceConflict
	SameScheduleOnly bool
}

// ConflictTarget 表示本次操作实际检查的发生身份。
type ConflictTarget struct {
	ScheduleID   ScheduleID
	OriginalDate string
	StartUnix    int64
}

// ConflictOccurrence 表示冲突某一侧的发生身份、标题和实际时间。
type ConflictOccurrence struct {
	ScheduleID   ScheduleID
	OriginalDate string
	Title        string
	StartUnix    int64
	EndUnix      int64
}

// OccurrenceConflict 表示目标发生与另一个身份发生之间的实际重叠。
type OccurrenceConflict struct {
	Target      ConflictOccurrence
	Other       ConflictOccurrence
	SameGroup   bool
	OverlapUnix int64
}

// ConflictSources 保存冲突检查需要读取的全部日程定义和例外快照。
type ConflictSources struct {
	Items []ConflictSource
}

// ConflictSource 表示单个日程的完整计算输入，是查询源快照的领域视图。
type ConflictSource struct {
	Schedule  Schedule
	Overrides []OccurrenceOverride
}

// SelectNextTargetBatch 返回所有日程中严格晚于 after 的最早一批发生。
// 同一开始时刻的全部身份都属于同一批次，关闭日程不产生目标。
// 它逐源流式推进，不展开全部未来发生，因此不受公开查询上界限制。
func SelectNextTargetBatch(sources ConflictSources, after time.Time, location *time.Location) ([]Occurrence, error) {
	if location == nil {
		return nil, NewValidation("time location must not be nil", "location")
	}
	candidates := make([]Occurrence, 0, len(sources.Items))
	for _, source := range sources.Items {
		occurrence, ok, err := nextSourceOccurrence(source, after, location)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, occurrence)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	// 实际时间按整秒比较，避免同一瞬间的不同单调钟表示被拆成两个批次。
	batchUnix := candidates[0].StartLocal.Unix()
	for _, candidate := range candidates[1:] {
		if candidate.StartLocal.Unix() < batchUnix {
			batchUnix = candidate.StartLocal.Unix()
		}
	}
	batch := make([]Occurrence, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.StartLocal.Unix() == batchUnix {
			batch = append(batch, candidate)
		}
	}
	sort.Slice(batch, func(i, j int) bool { return occurrenceLess(batch[i], batch[j]) })
	return batch, nil
}

// nextSourceOccurrence 返回单个日程严格晚于 after 的最早有效发生。
// 迭代器按实际区间过滤，因此以指数扩展的窗口推进，避免固定窗口漏掉更远的候选。
func nextSourceOccurrence(source ConflictSource, after time.Time, location *time.Location) (Occurrence, bool, error) {
	window := time.Hour
	for window <= 64*365*24*time.Hour {
		iterator, err := NewMergedOccurrenceIterator(source.Schedule, source.Overrides, after, after.Add(window), location)
		if err != nil {
			return Occurrence{}, false, err
		}
		occurrence, ok, err := iterator.Next()
		if err != nil {
			return Occurrence{}, false, err
		}
		if ok {
			return occurrence, true, nil
		}
		window *= 2
	}
	return Occurrence{}, false, nil
}

// DetectOccurrenceConflicts 使用半开区间重叠规则比较目标批次与窗口内的全部有效发生。
// 它只排除目标身份自身，同一父日程的其他发生仍然参与比较，并额外收集窗口前开始但跨入窗口的实例。
func DetectOccurrenceConflicts(sources ConflictSources, targets []Occurrence, targetStart, targetEnd time.Time, lookback time.Duration, location *time.Location) (ConflictCheck, error) {
	if location == nil {
		return ConflictCheck{}, NewValidation("time location must not be nil", "location")
	}
	if len(targets) == 0 {
		return ConflictCheck{Status: ConflictNoTarget}, nil
	}
	if lookback < 0 {
		lookback = 0
	}
	if !targetEnd.After(targetStart) {
		return ConflictCheck{}, NewValidation("conflict window end must be after start", "target_start", "target_end")
	}

	only := make(map[string]ConflictOccurrence, len(targets))
	check := ConflictCheck{Status: ConflictChecked, TargetUnix: targets[0].StartLocal.Unix(), SameScheduleOnly: true}
	for _, target := range targets {
		only[occurrenceKey(target.ScheduleID, target.OriginalDate)] = conflictOccurrence(target)
		check.Targets = append(check.Targets, ConflictTarget{
			ScheduleID: target.ScheduleID, OriginalDate: target.OriginalDate, StartUnix: target.StartLocal.Unix(),
		})
		if target.ScheduleID != targets[0].ScheduleID {
			check.SameScheduleOnly = false
		}
	}

	// 窗口前开始的实例可能横跨到目标批次内，因此检索下界必须回看最长持续时间。
	from := targetStart.Add(-lookback - time.Nanosecond)
	matches := make(conflictIndex)
	for _, source := range sources.Items {
		iterator, err := NewMergedOccurrenceIterator(source.Schedule, source.Overrides, from, targetEnd, location)
		if err != nil {
			return ConflictCheck{}, err
		}
		for {
			occurrence, ok, err := iterator.Next()
			if err != nil {
				return ConflictCheck{}, err
			}
			if !ok {
				break
			}
			matches.observe(occurrence, only)
		}
	}
	for _, conflict := range matches {
		check.Conflicts = append(check.Conflicts, conflict)
	}
	sort.Slice(check.Conflicts, func(i, j int) bool {
		left, right := check.Conflicts[i], check.Conflicts[j]
		if left.Other.StartUnix != right.Other.StartUnix {
			return left.Other.StartUnix < right.Other.StartUnix
		}
		return conflictOccurrenceKey(left.Other) < conflictOccurrenceKey(right.Other)
	})
	return check, nil
}

// OccurrencesOverlap 判断两个半开区间是否相交；边界相接不算冲突。
func OccurrencesOverlap(left, right Occurrence) bool {
	return left.StartLocal.Before(right.EndLocal) && right.StartLocal.Before(left.EndLocal)
}

// OccurrenceOverlaps 判断两个已知发生是否相交，用于把批次目标与扫描结果比较。
func OccurrenceOverlaps(left, right ConflictOccurrence) bool {
	return left.StartUnix < right.EndUnix && right.StartUnix < left.EndUnix
}

// conflictIndex 按目标身份收集冲突，避免同一对身份因重复扫描被记录多次。
type conflictIndex map[string]OccurrenceConflict

// observe 把一条有效发生与全部目标身份比较，并记录新的重叠。
func (index conflictIndex) observe(occurrence Occurrence, only map[string]ConflictOccurrence) {
	key := occurrenceKey(occurrence.ScheduleID, occurrence.OriginalDate)
	if target, isTarget := only[key]; isTarget {
		// 目标身份自身不参与比较；同一发生可能因例外移动而出现在其他时间点上。
		current, ok := conflictOccurrenceAt(occurrence, target)
		if !ok {
			return
		}
		for otherKey, candidate := range only {
			if otherKey == key || !OccurrenceOverlaps(current, candidate) {
				continue
			}
			index.record(otherKey, key, candidate, current)
		}
		return
	}
	current := conflictOccurrence(occurrence)
	for targetKey, target := range only {
		if !OccurrenceOverlaps(target, current) {
			continue
		}
		index.record(targetKey, key, target, current)
	}
}

// record 写入一对冲突并保留同一父日程的标识。
// 结果按身份排序键归一化，同一对身份只会产生一个条目和一个方向。
func (index conflictIndex) record(leftKey, rightKey string, left, right ConflictOccurrence) {
	if rightKey < leftKey {
		leftKey, rightKey, left, right = rightKey, leftKey, right, left
	}
	index[leftKey+"\x00"+rightKey] = OccurrenceConflict{
		Target:      left,
		Other:       right,
		SameGroup:   left.ScheduleID == right.ScheduleID,
		OverlapUnix: overlapUnix(left, right),
	}
}

// conflictOccurrenceAt 判断扫描结果是否就是目标身份本身的时间区间。
func conflictOccurrenceAt(occurrence Occurrence, target ConflictOccurrence) (ConflictOccurrence, bool) {
	current := conflictOccurrence(occurrence)
	return current, current.StartUnix == target.StartUnix && current.EndUnix == target.EndUnix
}

// conflictOccurrence 把可计算发生投影为冲突比较使用的身份和实际时间。
func conflictOccurrence(occurrence Occurrence) ConflictOccurrence {
	return ConflictOccurrence{
		ScheduleID:   occurrence.ScheduleID,
		OriginalDate: occurrence.OriginalDate,
		Title:        occurrence.Title,
		StartUnix:    occurrence.StartLocal.Unix(),
		EndUnix:      occurrence.EndLocal.Unix(),
	}
}

// conflictOccurrenceKey 返回冲突参与方的稳定排序身份。
func conflictOccurrenceKey(value ConflictOccurrence) string {
	return occurrenceKey(value.ScheduleID, value.OriginalDate)
}

// overlapUnix 返回两个相交区间的实际重叠起点。
func overlapUnix(left, right ConflictOccurrence) int64 {
	if left.StartUnix > right.StartUnix {
		return left.StartUnix
	}
	return right.StartUnix
}
