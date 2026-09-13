package domain

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// CronRule 表示严格每日、每周或每月规则；日期和星期字段互斥。
type CronRule struct {
	Minute     int
	Hour       int
	DayOfMonth *int
	Weekday    *time.Weekday
}

// Occurrence 是按需计算的发生，不是需要持久化的实例行。
type Occurrence struct {
	ScheduleID ScheduleID
	StartLocal time.Time
	EndLocal   time.Time
}

// ParseCron 只接受五字段的每日 M H * * *、每周 M H * * D 和每月 M H D * *，星期日为 0。
func ParseCron(expression string) (CronRule, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 || fields[3] != "*" {
		return CronRule{}, NewValidation("cron must be a daily, weekly, or monthly five-field rule", "cron")
	}
	minute, err := cronNumber(fields[0], 59)
	if err != nil {
		return CronRule{}, err
	}
	hour, err := cronNumber(fields[1], 23)
	if err != nil {
		return CronRule{}, err
	}
	rule := CronRule{Minute: minute, Hour: hour}
	if fields[2] != "*" {
		if fields[4] != "*" {
			return CronRule{}, NewValidation("cron cannot specify both day of month and weekday", "cron")
		}
		dayOfMonth, err := cronNumber(fields[2], 31)
		if err != nil || dayOfMonth == 0 {
			if err != nil {
				return CronRule{}, err
			}
			return CronRule{}, NewValidation("cron day of month is out of range", "cron")
		}
		rule.DayOfMonth = &dayOfMonth
		return rule, nil
	}
	if fields[4] != "*" {
		day, err := cronNumber(fields[4], 6)
		if err != nil {
			return CronRule{}, err
		}
		weekday := time.Weekday(day)
		rule.Weekday = &weekday
	}
	return rule, nil
}

// cronNumber 拒绝符号、别名、范围、列表、步长和通配符，只接受范围内的十进制数字。
func cronNumber(field string, maximum int) (int, error) {
	for _, c := range field {
		if c < '0' || c > '9' {
			return 0, NewValidation("cron fields must be decimal integers", "cron")
		}
	}
	n, err := strconv.Atoi(field)
	if err != nil || n < 0 || n > maximum {
		return 0, NewValidation("cron field is out of range", "cron")
	}
	return n, nil
}

// ValidateRecurring 校验首次端点、重复规则和包含式截止日期，生成重复日程候选。
// 重复定义允许历史首次时间，计算不能被当前日期截断；截止日期不还原为午夜瞬间。
func ValidateRecurring(snapshot TimeSnapshot, startUnix, endUnix int64, cron, untilDateLocal string) (ScheduleCandidate, error) {
	if snapshot.Location == nil {
		return ScheduleCandidate{}, NewValidation("time location must not be nil", "location")
	}
	rule, err := ParseCron(cron)
	if err != nil {
		return ScheduleCandidate{}, err
	}
	if startUnix%60 != 0 || endUnix%60 != 0 {
		return ScheduleCandidate{}, NewValidation("endpoints must have minute precision", "start_unix", "end_unix")
	}
	// 先检查差值的上溢，再转为分钟；避免极端 Unix 输入绕过正时长校验。
	if endUnix <= startUnix || (startUnix < 0 && endUnix > math.MaxInt64+startUnix) {
		return ScheduleCandidate{}, NewValidation("schedule duration is invalid", "start_unix", "end_unix")
	}
	minutes := (endUnix - startUnix) / 60
	if minutes > math.MaxInt64/int64(time.Minute) || int64(int(minutes)) != minutes {
		return ScheduleCandidate{}, NewValidation("schedule duration is out of range", "start_unix", "end_unix")
	}
	start := time.Unix(startUnix, 0).In(snapshot.Location)
	until, err := time.Parse(time.DateOnly, untilDateLocal)
	if err != nil || until.Year() < 1 || until.Year() > 9999 {
		return ScheduleCandidate{}, NewValidation("cutoff date is invalid", "until_date_local")
	}
	if start.Year() < 1 || start.Year() > 9999 {
		return ScheduleCandidate{}, NewValidation("local date is out of range", "start_unix", "until_unix")
	}
	if untilDateLocal < start.Format(time.DateOnly) {
		return ScheduleCandidate{}, NewValidation("cutoff date precedes first start date", "until_unix")
	}
	return ScheduleCandidate{Cron: &cron, UntilDateLocal: &untilDateLocal, MonthEndPolicy: monthEndPolicyForRule(rule), Open: true,
		FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: int(minutes),
		CreatedAtUTC: snapshot.Now.UTC(), UpdatedAtUTC: snapshot.Now.UTC()}, nil
}

// recurrenceInputs 校验已保存的计算输入，并以显式时区解释首次墙钟和截止日。
func recurrenceInputs(schedule Schedule, location *time.Location) (CronRule, time.Time, time.Time, error) {
	if location == nil {
		return CronRule{}, time.Time{}, time.Time{}, NewValidation("time location must not be nil", "location")
	}
	if schedule.Cron == nil || schedule.UntilDateLocal == nil {
		return CronRule{}, time.Time{}, time.Time{}, NewValidation("recurring rule and cutoff are required", "cron", "until_date_local")
	}
	rule, err := ParseCron(*schedule.Cron)
	if err != nil {
		return CronRule{}, time.Time{}, time.Time{}, err
	}
	if err := validateMonthEndPolicy(rule, schedule.MonthEndPolicy); err != nil {
		return CronRule{}, time.Time{}, time.Time{}, err
	}
	until, err := time.Parse(time.DateOnly, *schedule.UntilDateLocal)
	if err != nil {
		return CronRule{}, time.Time{}, time.Time{}, NewValidation("cutoff date is invalid", "until_date_local")
	}
	first := schedule.FirstStartLocal
	if first.Year() < 1 || first.Year() > 9999 || first.Second() != 0 || first.Nanosecond() != 0 || schedule.DurationMinutes <= 0 || int64(schedule.DurationMinutes) > math.MaxInt64/int64(time.Minute) {
		return CronRule{}, time.Time{}, time.Time{}, NewValidation("stored recurrence is invalid", "first_start_local", "duration_minutes")
	}
	first = resolveWallTime(first.Year(), first.Month(), first.Day(), first.Hour(), first.Minute(), location)
	if *schedule.UntilDateLocal < first.Format(time.DateOnly) {
		return CronRule{}, time.Time{}, time.Time{}, NewValidation("cutoff date precedes first start date", "until_date_local")
	}
	return rule, first, until, nil
}

// NextBase 返回严格晚于 after 的下一次规则发生；首次匹配和截止日期均包含。
func NextBase(schedule Schedule, after time.Time, location *time.Location) (Occurrence, bool, error) {
	rule, first, until, err := recurrenceInputs(schedule, location)
	if err != nil {
		return Occurrence{}, false, err
	}
	occurrence, ok := nextBase(schedule, rule, first, until, after, location)
	return occurrence, ok, nil
}

// nextBase 从查询日期附近按自然日推进，不保存游标，支持任意历史重算。
func nextBase(schedule Schedule, rule CronRule, first, until, after time.Time, location *time.Location) (Occurrence, bool) {
	if rule.DayOfMonth != nil {
		return nextMonthlyBase(schedule, rule, first, until, after, location)
	}
	date := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
	localAfter := after.In(location)
	// 回看一天覆盖跨日期跳变后移的候选；仍由实际时间做严格 after 判断。
	afterDate := time.Date(localAfter.Year(), localAfter.Month(), localAfter.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if afterDate.After(date) {
		date = afterDate
	}
	step := 1
	if rule.Weekday != nil {
		date = date.AddDate(0, 0, (int(*rule.Weekday)-int(date.Weekday())+7)%7)
		step = 7
	}
	for !date.After(until) {
		start := resolveWallTime(date.Year(), date.Month(), date.Day(), rule.Hour, rule.Minute, location)
		if !start.Before(first) && start.After(after) {
			return Occurrence{ScheduleID: schedule.ID, StartLocal: start, EndLocal: start.Add(time.Duration(schedule.DurationMinutes) * time.Minute)}, true
		}
		date = date.AddDate(0, 0, step)
	}
	return Occurrence{}, false
}

// nextMonthlyBase 按月份首日推进月规则，每次重新计算短月中的有效日期。
func nextMonthlyBase(schedule Schedule, rule CronRule, first, until, after time.Time, location *time.Location) (Occurrence, bool) {
	firstMonth := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	localAfter := after.In(location)
	date := time.Date(localAfter.Year(), localAfter.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if date.Before(firstMonth) {
		date = firstMonth
	}
	untilMonth := time.Date(until.Year(), until.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !date.After(untilMonth) {
		nextMonth := date.AddDate(0, 1, 0)
		lastDay := nextMonth.AddDate(0, 0, -1).Day()
		day := *rule.DayOfMonth
		if day > lastDay {
			day = lastDay
		}
		candidateDate := time.Date(date.Year(), date.Month(), day, 0, 0, 0, 0, time.UTC)
		start := resolveWallTime(date.Year(), date.Month(), day, rule.Hour, rule.Minute, location)
		// 先按原始墙钟发生日判断截止，再用实际时间执行包含式首次和严格 after。
		if !candidateDate.After(until) && !start.Before(first) && start.After(after) {
			return Occurrence{ScheduleID: schedule.ID, StartLocal: start, EndLocal: start.Add(time.Duration(schedule.DurationMinutes) * time.Minute)}, true
		}
		date = nextMonth
	}
	return Occurrence{}, false
}

// monthEndPolicyForRule 为月规则生成领域内部约定的持久化策略。
func monthEndPolicyForRule(rule CronRule) *string {
	if rule.DayOfMonth == nil {
		return nil
	}
	policy := MonthEndPolicyClamp
	return &policy
}

// validateMonthEndPolicy 校验已保存规则和月末策略的组合一致性。
func validateMonthEndPolicy(rule CronRule, policy *string) error {
	if rule.DayOfMonth != nil {
		if policy == nil || *policy != MonthEndPolicyClamp {
			return NewValidation("monthly recurrence requires clamp month end policy", "month_end_policy")
		}
		return nil
	}
	if policy != nil {
		return NewValidation("month end policy is only valid for monthly recurrence", "month_end_policy")
	}
	return nil
}

// Expand 展开开始时刻落在 [from, to) 的发生；不读取时钟、不修改定义、不写实例。
func Expand(schedule Schedule, from, to time.Time, location *time.Location) ([]Occurrence, error) {
	rule, first, until, err := recurrenceInputs(schedule, location)
	if err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, NewValidation("expansion end must be after start", "from", "to")
	}
	result := make([]Occurrence, 0)
	after := from.Add(-time.Nanosecond)
	for {
		occurrence, ok := nextBase(schedule, rule, first, until, after, location)
		if !ok || !occurrence.StartLocal.Before(to) {
			return result, nil
		}
		result = append(result, occurrence)
		after = occurrence.StartLocal
	}
}

// NextStart 返回严格晚于 after 的下一开始时刻；没有后续发生时返回 false。
func NextStart(schedule Schedule, after time.Time, location *time.Location) (time.Time, bool, error) {
	occurrence, ok, err := NextBase(schedule, after, location)
	return occurrence.StartLocal, ok, err
}

// resolveWallTime 按时区区间反推实际时刻，重叠取较早值，缺失按跳变幅度后移。
func resolveWallTime(year int, month time.Month, day, hour, minute int, location *time.Location) time.Time {
	wall := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	var earliest, shifted time.Time
	// 时区偏移和日期线跳变均落在此窗口内；按区间跳转，避免逐分钟扫描。
	end := wall.Add(48 * time.Hour)
	for probe := wall.Add(-48 * time.Hour); !probe.After(end); {
		local := probe.In(location)
		_, offset := local.Zone()
		candidate := wall.Add(-time.Duration(offset) * time.Second).In(location)
		civil := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour(), candidate.Minute(), candidate.Second(), 0, time.UTC)
		if civil.Equal(wall) && (earliest.IsZero() || candidate.Before(earliest)) {
			earliest = candidate
		}
		if civil.After(wall) && (shifted.IsZero() || candidate.Before(shifted)) {
			shifted = candidate
		}
		_, bound := local.ZoneBounds()
		if bound.IsZero() || !bound.After(probe) {
			break
		}
		probe = bound
	}
	if !earliest.IsZero() {
		return earliest
	}
	return shifted
}
