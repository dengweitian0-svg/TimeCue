package domain

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ValidateOccurrenceOverride 校验某次发生的完整修改快照，不应用单次日程的一年范围限制。
func ValidateOccurrenceOverride(snapshot TimeSnapshot, originalDate string, startUnix, endUnix int64, title, notes string) (OccurrenceOverrideCandidate, error) {
	if snapshot.Location == nil {
		return OccurrenceOverrideCandidate{}, NewValidation("time location must not be nil", "location")
	}
	if _, err := parseOccurrenceDate(originalDate); err != nil {
		return OccurrenceOverrideCandidate{}, err
	}
	if startUnix%60 != 0 {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence start time must have minute precision", "start_unix")
	}
	if endUnix%60 != 0 {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence end time must have minute precision", "end_unix")
	}
	if endUnix <= startUnix {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence end time must be after start time", "start_unix", "end_unix")
	}
	if startUnix < 0 && endUnix > math.MaxInt64+startUnix {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence duration is out of range", "start_unix", "end_unix")
	}
	durationSeconds := endUnix - startUnix
	if durationSeconds%60 != 0 || durationSeconds/60 > math.MaxInt64/int64(time.Minute) {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence duration is out of range", "start_unix", "end_unix")
	}
	durationMinutes := durationSeconds / 60
	if durationMinutes <= 0 || int64(int(durationMinutes)) != durationMinutes {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence duration is out of range", "start_unix", "end_unix")
	}
	if strings.TrimSpace(title) == "" {
		return OccurrenceOverrideCandidate{}, NewValidation("title must not be empty", "title")
	}
	start := time.Unix(startUnix, 0).In(snapshot.Location)
	end := time.Unix(endUnix, 0).In(snapshot.Location)
	if start.Year() < 1 || start.Year() > 9999 || end.Year() < 1 || end.Year() > 9999 {
		return OccurrenceOverrideCandidate{}, NewValidation("occurrence time is out of range", "start_unix", "end_unix")
	}
	return OccurrenceOverrideCandidate{
		OriginalDate:    originalDate,
		StartLocal:      start,
		DurationMinutes: int(durationMinutes),
		Title:           title,
		Notes:           notes,
		UpdatedAtUTC:    snapshot.Now.UTC(),
	}, nil
}

// MergeOccurrences 合并基础发生和例外快照，并按实际开始时间稳定排序。
func MergeOccurrences(base []Occurrence, overrides []OccurrenceOverride) []Occurrence {
	effective := make(map[string]Occurrence, len(base)+len(overrides))
	for _, occurrence := range base {
		effective[occurrenceKey(occurrence.ScheduleID, occurrence.OriginalDate)] = occurrence
	}
	for _, override := range overrides {
		key := occurrenceKey(override.ScheduleID, override.OriginalDate)
		delete(effective, key)
		if !override.Open || !override.SnapshotPresent {
			continue
		}
		effective[key] = Occurrence{
			ScheduleID:   override.ScheduleID,
			OriginalDate: override.OriginalDate,
			StartLocal:   override.StartLocal,
			EndLocal:     override.StartLocal.Add(time.Duration(override.DurationMinutes) * time.Minute),
			Title:        override.Title,
			Notes:        override.Notes,
		}
	}
	result := make([]Occurrence, 0, len(effective))
	for _, occurrence := range effective {
		result = append(result, occurrence)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartLocal.Equal(result[j].StartLocal) {
			return occurrenceKey(result[i].ScheduleID, result[i].OriginalDate) < occurrenceKey(result[j].ScheduleID, result[j].OriginalDate)
		}
		return result[i].StartLocal.Before(result[j].StartLocal)
	})
	return result
}

// NextMergedOccurrence 返回严格晚于 after 的最早有效基础或例外发生。
func NextMergedOccurrence(schedule Schedule, overrides []OccurrenceOverride, after time.Time, location *time.Location) (Occurrence, bool, error) {
	if _, _, _, err := recurrenceInputs(schedule, location); err != nil {
		return Occurrence{}, false, err
	}
	if !schedule.Open {
		return Occurrence{}, false, nil
	}
	overridden := make(map[string]struct{}, len(overrides))
	for _, override := range overrides {
		if override.ScheduleID == schedule.ID {
			overridden[occurrenceKey(override.ScheduleID, override.OriginalDate)] = struct{}{}
		}
	}
	var baseOccurrence Occurrence
	baseAfter := after
	for {
		candidate, ok, err := NextBase(schedule, baseAfter, location)
		if err != nil {
			return Occurrence{}, false, err
		}
		if !ok {
			break
		}
		if _, replaced := overridden[occurrenceKey(candidate.ScheduleID, candidate.OriginalDate)]; !replaced {
			baseOccurrence = candidate
			break
		}
		baseAfter = candidate.StartLocal
	}

	var overrideOccurrence Occurrence
	for _, override := range overrides {
		if override.ScheduleID != schedule.ID || !override.Open || !override.SnapshotPresent || !override.StartLocal.After(after) {
			continue
		}
		candidate := Occurrence{
			ScheduleID:   override.ScheduleID,
			OriginalDate: override.OriginalDate,
			StartLocal:   override.StartLocal,
			EndLocal:     override.StartLocal.Add(time.Duration(override.DurationMinutes) * time.Minute),
			Title:        override.Title,
			Notes:        override.Notes,
		}
		if overrideOccurrence.StartLocal.IsZero() || occurrenceLess(candidate, overrideOccurrence) {
			overrideOccurrence = candidate
		}
	}
	if baseOccurrence.StartLocal.IsZero() {
		return overrideOccurrence, !overrideOccurrence.StartLocal.IsZero(), nil
	}
	if overrideOccurrence.StartLocal.IsZero() || occurrenceLess(baseOccurrence, overrideOccurrence) {
		return baseOccurrence, true, nil
	}
	return overrideOccurrence, true, nil
}

// ExpandMergedOccurrences 展开查询窗口内的基础发生并合入实际时间窗口内的例外。
func ExpandMergedOccurrences(schedule Schedule, overrides []OccurrenceOverride, from, to time.Time, location *time.Location) ([]Occurrence, error) {
	iterator, err := NewMergedOccurrenceIterator(schedule, overrides, from, to, location)
	if err != nil {
		return nil, err
	}
	result := make([]Occurrence, 0)
	for {
		occurrence, ok, nextErr := iterator.Next()
		if nextErr != nil {
			return nil, nextErr
		}
		if !ok {
			return result, nil
		}
		result = append(result, occurrence)
	}
}

// MergedOccurrenceIterator 按实际时间流式合并某个日程的基础发生和例外。
// 它只保留例外索引和当前候选，不会为多年查询一次性生成全部发生。
type MergedOccurrenceIterator struct {
	schedule        Schedule
	overrides       map[string]OccurrenceOverride
	from            time.Time
	to              time.Time
	location        *time.Location
	after           time.Time
	singlePending   bool
	afterOccurrence *Occurrence
}

// NewMergedOccurrenceIterator 创建查询窗口内的无副作用发生迭代器。
func NewMergedOccurrenceIterator(schedule Schedule, overrides []OccurrenceOverride, from, to time.Time, location *time.Location) (*MergedOccurrenceIterator, error) {
	if location == nil {
		return nil, NewValidation("time location must not be nil", "location")
	}
	if !to.After(from) {
		return nil, NewValidation("expansion end must be after start", "from", "to")
	}
	if schedule.Cron != nil {
		if _, _, _, err := recurrenceInputs(schedule, location); err != nil {
			return nil, err
		}
	} else if schedule.FirstStartLocal.IsZero() || schedule.DurationMinutes <= 0 {
		return nil, NewValidation("stored single schedule is invalid", "first_start_local", "duration_minutes")
	}
	indexed := make(map[string]OccurrenceOverride, len(overrides))
	for _, override := range overrides {
		if override.ScheduleID == schedule.ID {
			indexed[occurrenceKey(override.ScheduleID, override.OriginalDate)] = override
		}
	}
	lookbackMinutes := schedule.DurationMinutes
	for _, override := range overrides {
		if override.ScheduleID == schedule.ID && override.DurationMinutes > lookbackMinutes {
			lookbackMinutes = override.DurationMinutes
		}
	}
	duration := time.Duration(lookbackMinutes) * time.Minute
	return &MergedOccurrenceIterator{
		schedule: schedule, overrides: indexed, from: from, to: to, location: location,
		after: from.Add(-duration - time.Nanosecond), singlePending: schedule.Cron == nil,
	}, nil
}

// Next 返回下一条实际区间相交的发生；结果耗尽时返回 false。
func (i *MergedOccurrenceIterator) Next() (Occurrence, bool, error) {
	if i == nil {
		return Occurrence{}, false, NewValidation("occurrence iterator must not be nil")
	}
	if !i.schedule.Open {
		return Occurrence{}, false, nil
	}

	cursor := i.after
	override, overrideOK := i.nextOverride()
	for {

		var base Occurrence
		var baseOK bool
		var err error
		if i.singlePending {
			i.singlePending = false
			start := i.schedule.FirstStartLocal.In(i.location)
			base = Occurrence{ScheduleID: i.schedule.ID, OriginalDate: start.Format(time.DateOnly), StartLocal: start,
				EndLocal: start.Add(time.Duration(i.schedule.DurationMinutes) * time.Minute), Title: i.schedule.Title, Notes: i.schedule.Notes}
			baseOK = true
		} else if i.schedule.Cron != nil {
			base, baseOK, err = NextBase(i.schedule, cursor, i.location)
			if err != nil {
				return Occurrence{}, false, err
			}
		}
		if !baseOK {
			if !overrideOK {
				return Occurrence{}, false, nil
			}
			i.after = override.StartLocal
			return override, true, nil
		}
		if _, replaced := i.overrides[occurrenceKey(base.ScheduleID, base.OriginalDate)]; replaced {
			cursor = base.StartLocal
			continue
		}
		if !Overlaps(base.StartLocal, base.EndLocal, i.from, i.to) {
			// 基础发生可能从窗口前开始；逐条回看直到找到相交区间或越过窗口。
			if !base.StartLocal.Before(i.to) {
				if !overrideOK {
					return Occurrence{}, false, nil
				}
				i.after = override.StartLocal
				return override, true, nil
			}
			cursor = base.StartLocal
			continue
		}
		if overrideOK && occurrenceLess(override, base) {
			i.after = override.StartLocal
			return override, true, nil
		}
		i.after = base.StartLocal
		copy := base
		i.afterOccurrence = &copy
		return base, true, nil
	}
}

// nextOverride 返回游标之后、查询窗口内最早的有效移动例外。
func (i *MergedOccurrenceIterator) nextOverride() (Occurrence, bool) {
	var result Occurrence
	for _, override := range i.overrides {
		if !override.Open || !override.SnapshotPresent {
			continue
		}
		candidate := Occurrence{ScheduleID: override.ScheduleID, OriginalDate: override.OriginalDate,
			StartLocal: override.StartLocal, EndLocal: override.StartLocal.Add(time.Duration(override.DurationMinutes) * time.Minute),
			Title: override.Title, Notes: override.Notes}
		if i.afterOccurrence != nil {
			if !occurrenceLess(*i.afterOccurrence, candidate) {
				continue
			}
		} else if !candidate.StartLocal.After(i.after) {
			continue
		}
		if !Overlaps(candidate.StartLocal, candidate.EndLocal, i.from, i.to) {
			continue
		}
		if result.StartLocal.IsZero() || occurrenceLess(candidate, result) {
			result = candidate
		}
	}
	return result, !result.StartLocal.IsZero()
}

// Overlaps 判断两个半开时间区间是否有实际交集；边界相接不算相交。
func Overlaps(start, end, from, to time.Time) bool {
	return start.Before(to) && end.After(from)
}

// OccurrenceOnDate 返回规则在指定原始本地日期上的基础发生，用于判断例外是否有合法父身份。
func OccurrenceOnDate(schedule Schedule, originalDate string, location *time.Location) (Occurrence, bool, error) {
	date, err := parseOccurrenceDate(originalDate)
	if err != nil {
		return Occurrence{}, false, err
	}
	if location == nil {
		return Occurrence{}, false, NewValidation("time location must not be nil", "location")
	}
	from := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 1)
	// 原始日期身份匹配必须独立于父组 Open 状态，供关闭组恢复本次例外。
	schedule.Open = true
	occurrences, err := Expand(schedule, from, to, location)
	if err != nil {
		return Occurrence{}, false, err
	}
	for _, occurrence := range occurrences {
		if occurrence.OriginalDate == originalDate {
			return occurrence, true, nil
		}
	}
	return Occurrence{}, false, nil
}

// parseOccurrenceDate 解析并规范化例外使用的本地日期身份。
func parseOccurrenceDate(value string) (time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Year() < 1 || parsed.Year() > 9999 {
		return time.Time{}, NewValidation("original date is invalid", "original_date")
	}
	return parsed, nil
}

// occurrenceKey 组合日程和本地日期，避免不同日程的同日发生互相覆盖。
func occurrenceKey(id ScheduleID, originalDate string) string {
	return strconv.FormatInt(int64(id), 10) + "\x00" + originalDate
}

// occurrenceLess 比较实际开始时间并在同刻时使用稳定身份排序。
func occurrenceLess(left, right Occurrence) bool {
	if left.StartLocal.Equal(right.StartLocal) {
		return occurrenceKey(left.ScheduleID, left.OriginalDate) < occurrenceKey(right.ScheduleID, right.OriginalDate)
	}
	return left.StartLocal.Before(right.StartLocal)
}
