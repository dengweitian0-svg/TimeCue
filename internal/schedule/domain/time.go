package domain

import (
	"fmt"
	"time"
)

const localTimeLayout = time.RFC3339Nano

// TimeSnapshot 保存一次请求使用的当前时间和时区。
type TimeSnapshot struct {
	Now      time.Time
	Location *time.Location
}

// TimeBounds 描述单次保存和未来查询的时间边界。
type TimeBounds struct {
	SingleMin          time.Time
	SingleMaxExclusive time.Time
	QueryMaxExclusive  time.Time
}

// CalculateTimeBounds 根据时间快照计算日程边界。
func CalculateTimeBounds(snapshot TimeSnapshot) TimeBounds {
	location := snapshot.Location
	if location == nil {
		location = time.UTC
	}

	// 先将当前时刻转换为本地日期，日期边界不能直接从 UTC 日期推导。
	localNow := snapshot.Now.In(location)
	localDateStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)

	// 单次日程从当前本地日期开始，未来查询额外覆盖上界所在的完整本地日期。
	singleMaxExclusive := AddCalendarYearClamped(localDateStart)
	queryMaxExclusive := singleMaxExclusive.AddDate(0, 0, 1)
	return TimeBounds{
		SingleMin:          localDateStart,
		SingleMaxExclusive: singleMaxExclusive,
		QueryMaxExclusive:  queryMaxExclusive,
	}
}

// ValidateSingleEndpoints 校验单次日程端点并生成存储候选。
func ValidateSingleEndpoints(snapshot TimeSnapshot, startUnix, endUnix int64) (ScheduleCandidate, error) {
	if snapshot.Location == nil {
		return ScheduleCandidate{}, NewValidation("time location must not be nil", "location")
	}
	if startUnix%60 != 0 {
		return ScheduleCandidate{}, NewValidation("start time must have minute precision", "start_unix")
	}
	if endUnix%60 != 0 {
		return ScheduleCandidate{}, NewValidation("end time must have minute precision", "end_unix")
	}
	if endUnix <= startUnix {
		return ScheduleCandidate{}, NewValidation("end time must be after start time", "start_unix", "end_unix")
	}

	startLocal := time.Unix(startUnix, 0).In(snapshot.Location)
	endLocal := time.Unix(endUnix, 0).In(snapshot.Location)
	bounds := CalculateTimeBounds(snapshot)
	if startLocal.Before(bounds.SingleMin) || !startLocal.Before(bounds.SingleMaxExclusive) ||
		endLocal.After(bounds.SingleMaxExclusive) {
		return ScheduleCandidate{}, NewTimeRangeExceeded([]string{"start_unix", "end_unix"}, bounds)
	}

	durationSeconds := endUnix - startUnix
	if durationSeconds%60 != 0 {
		return ScheduleCandidate{}, NewValidation("schedule duration must have minute precision", "start_unix", "end_unix")
	}
	durationMinutes := int(durationSeconds / 60)
	if durationMinutes <= 0 || int64(durationMinutes)*60 != durationSeconds {
		return ScheduleCandidate{}, NewValidation("schedule duration is invalid", "start_unix", "end_unix")
	}

	// 端点校验完成后才允许上层进入存储副作用；单次日程不携带 Cron。
	createdAtUTC := snapshot.Now.UTC()
	return ScheduleCandidate{
		Cron:               nil,
		Open:               true,
		FirstStartLocal:    startLocal,
		OriginalStartLocal: startLocal,
		DurationMinutes:    durationMinutes,
		CreatedAtUTC:       createdAtUTC,
		UpdatedAtUTC:       createdAtUTC,
	}, nil
}

// AddCalendarYearClamped 执行带闰日夹取的日历年加法。
func AddCalendarYearClamped(value time.Time) time.Time {
	// time.AddDate 会把闰年的 2 月 29 日规范化到 3 月 1 日，这里必须保留“同月同日”的边界语义。
	if value.Month() == time.February && value.Day() == 29 {
		return time.Date(value.Year()+1, time.February, 28, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location())
	}
	return value.AddDate(1, 0, 0)
}

// FormatLocalTime 将本地墙钟时间格式化为持久化格式。
func FormatLocalTime(value time.Time) string {
	return value.Format(localTimeLayout)
}

// parseLocalTime 解析存储中的本地时间文本并保留文本携带的时区偏移。
func parseLocalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(localTimeLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse local time %q: %w", value, err)
	}
	return parsed, nil
}
