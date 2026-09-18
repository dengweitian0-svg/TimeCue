package domain

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CronRule 表示严格每日、每周或每月规则；日期和星期字段互斥。
type CronRule struct {
	Minute     int
	Hour       int
	DayOfMonth *int
	Weekdays   []time.Weekday
}

// ParseCron 只接受五字段的每日 M H * * *、每周 M H * * D[,D...] 和每月 M H D * *，星期日为 0。
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
		weekdays, err := parseWeekdays(fields[4])
		if err != nil {
			return CronRule{}, err
		}
		rule.Weekdays = weekdays
	}
	return rule, nil
}

// parseWeekdays 解析星期列表，并以升序去重后的数值保留规则语义。
func parseWeekdays(field string) ([]time.Weekday, error) {
	parts := strings.Split(field, ",")
	seen := make(map[int]struct{}, len(parts))
	weekdays := make([]time.Weekday, 0, len(parts))
	for _, part := range parts {
		day, err := cronNumber(part, 6)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[day]; exists {
			continue
		}
		seen[day] = struct{}{}
		weekdays = append(weekdays, time.Weekday(day))
	}
	sort.Slice(weekdays, func(i, j int) bool { return weekdays[i] < weekdays[j] })
	return weekdays, nil
}

// NormalizeCron 返回规则的稳定五字段表示，星期列表按升序去重。
func NormalizeCron(expression string) (string, error) {
	rule, err := ParseCron(expression)
	if err != nil {
		return "", err
	}
	return canonicalCron(rule), nil
}

// canonicalCron 将已校验规则编码为稳定的应用和存储表示。
func canonicalCron(rule CronRule) string {
	if rule.DayOfMonth != nil {
		return strconv.Itoa(rule.Minute) + " " + strconv.Itoa(rule.Hour) + " " + strconv.Itoa(*rule.DayOfMonth) + " * *"
	}
	if len(rule.Weekdays) == 0 {
		return strconv.Itoa(rule.Minute) + " " + strconv.Itoa(rule.Hour) + " * * *"
	}
	weekdays := make([]string, len(rule.Weekdays))
	for index, weekday := range rule.Weekdays {
		weekdays[index] = strconv.Itoa(int(weekday))
	}
	return strconv.Itoa(rule.Minute) + " " + strconv.Itoa(rule.Hour) + " * * " + strings.Join(weekdays, ",")
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

// ValidateRecurring 校验首次端点、重复规则和可选截止日期，生成重复日程候选。
// 重复定义允许历史首次时间；截止日期不还原为午夜瞬间，nil 表示无限期重复。
func ValidateRecurring(snapshot TimeSnapshot, startUnix, endUnix int64, cron string, untilDateLocal *string) (ScheduleCandidate, error) {
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
	if start.Year() < 1 || start.Year() > 9999 {
		return ScheduleCandidate{}, NewValidation("local date is out of range", "start_unix", "until_unix")
	}
	if untilDateLocal != nil {
		until, parseErr := time.Parse(time.DateOnly, *untilDateLocal)
		if parseErr != nil || until.Year() < 1 || until.Year() > 9999 {
			return ScheduleCandidate{}, NewValidation("cutoff date is invalid", "until_date_local")
		}
		if *untilDateLocal < start.Format(time.DateOnly) {
			return ScheduleCandidate{}, NewValidation("cutoff date precedes first start date", "until_unix")
		}
	}
	normalizedCron := canonicalCron(rule)
	return ScheduleCandidate{Cron: &normalizedCron, UntilDateLocal: untilDateLocal, MonthEndPolicy: monthEndPolicyForRule(rule), Open: true,
		FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: int(minutes),
		CreatedAtUTC: snapshot.Now.UTC(), UpdatedAtUTC: snapshot.Now.UTC()}, nil
}

// recurrenceInputs 校验已保存的计算输入，并以显式时区解释首次墙钟和可选截止日。
func recurrenceInputs(schedule Schedule, location *time.Location) (CronRule, time.Time, *time.Time, error) {
	if location == nil {
		return CronRule{}, time.Time{}, nil, NewValidation("time location must not be nil", "location")
	}
	if schedule.Cron == nil {
		return CronRule{}, time.Time{}, nil, NewValidation("recurring rule is required", "cron")
	}
	rule, err := ParseCron(*schedule.Cron)
	if err != nil {
		return CronRule{}, time.Time{}, nil, err
	}
	if err := validateMonthEndPolicy(rule, schedule.MonthEndPolicy); err != nil {
		return CronRule{}, time.Time{}, nil, err
	}
	var until *time.Time
	if schedule.UntilDateLocal != nil {
		parsed, parseErr := time.Parse(time.DateOnly, *schedule.UntilDateLocal)
		if parseErr != nil {
			return CronRule{}, time.Time{}, nil, NewValidation("cutoff date is invalid", "until_date_local")
		}
		until = &parsed
	}
	first := schedule.FirstStartLocal
	if first.Year() < 1 || first.Year() > 9999 || first.Second() != 0 || first.Nanosecond() != 0 || schedule.DurationMinutes <= 0 || int64(schedule.DurationMinutes) > math.MaxInt64/int64(time.Minute) {
		return CronRule{}, time.Time{}, nil, NewValidation("stored recurrence is invalid", "first_start_local", "duration_minutes")
	}
	first = resolveWallTime(first.Year(), first.Month(), first.Day(), first.Hour(), first.Minute(), location)
	if schedule.UntilDateLocal != nil && *schedule.UntilDateLocal < first.Format(time.DateOnly) {
		return CronRule{}, time.Time{}, nil, NewValidation("cutoff date precedes first start date", "until_date_local")
	}
	return rule, first, until, nil
}

// NextBase 返回严格晚于 after 的下一次规则发生；首次匹配和截止日期均包含。
func NextBase(schedule Schedule, after time.Time, location *time.Location) (Occurrence, bool, error) {
	rule, first, until, err := recurrenceInputs(schedule, location)
	if err != nil {
		return Occurrence{}, false, err
	}
	if !schedule.Open {
		return Occurrence{}, false, nil
	}
	occurrence, ok := nextBase(schedule, rule, first, until, after, location)
	return occurrence, ok, nil
}

// nextBase 从查询日期附近按自然日推进，不保存游标，支持任意历史重算。
func nextBase(schedule Schedule, rule CronRule, first time.Time, until *time.Time, after time.Time, location *time.Location) (Occurrence, bool) {
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
	for until == nil || !date.After(*until) {
		start := resolveWallTime(date.Year(), date.Month(), date.Day(), rule.Hour, rule.Minute, location)
		if !start.Before(first) && start.After(after) && weekdayMatches(rule.Weekdays, date.Weekday()) {
			return Occurrence{
				ScheduleID:   schedule.ID,
				OriginalDate: date.Format(time.DateOnly),
				StartLocal:   start,
				EndLocal:     start.Add(time.Duration(schedule.DurationMinutes) * time.Minute),
				Title:        schedule.Title,
				Notes:        schedule.Notes,
			}, true
		}
		date = date.AddDate(0, 0, 1)
	}
	return Occurrence{}, false
}

// weekdayMatches 判断每日规则或多星期规则是否命中当前自然日。
func weekdayMatches(weekdays []time.Weekday, weekday time.Weekday) bool {
	if len(weekdays) == 0 {
		return true
	}
	for _, candidate := range weekdays {
		if candidate == weekday {
			return true
		}
	}
	return false
}

// nextMonthlyBase 按月份首日推进月规则，每次重新计算短月中的有效日期。
func nextMonthlyBase(schedule Schedule, rule CronRule, first time.Time, until *time.Time, after time.Time, location *time.Location) (Occurrence, bool) {
	firstMonth := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	localAfter := after.In(location)
	date := time.Date(localAfter.Year(), localAfter.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if date.Before(firstMonth) {
		date = firstMonth
	}
	var untilMonth time.Time
	if until != nil {
		untilMonth = time.Date(until.Year(), until.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	for until == nil || !date.After(untilMonth) {
		nextMonth := date.AddDate(0, 1, 0)
		lastDay := nextMonth.AddDate(0, 0, -1).Day()
		day := *rule.DayOfMonth
		if day > lastDay {
			day = lastDay
		}
		candidateDate := time.Date(date.Year(), date.Month(), day, 0, 0, 0, 0, time.UTC)
		start := resolveWallTime(date.Year(), date.Month(), day, rule.Hour, rule.Minute, location)
		// 先按原始墙钟发生日判断截止，再用实际时间执行包含式首次和严格 after。
		if (until == nil || !candidateDate.After(*until)) && !start.Before(first) && start.After(after) {
			return Occurrence{
				ScheduleID:   schedule.ID,
				OriginalDate: candidateDate.Format(time.DateOnly),
				StartLocal:   start,
				EndLocal:     start.Add(time.Duration(schedule.DurationMinutes) * time.Minute),
				Title:        schedule.Title,
				Notes:        schedule.Notes,
			}, true
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
	if !schedule.Open {
		return nil, nil
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
