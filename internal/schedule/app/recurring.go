package app

import (
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// prepareRecurringUpdate 合并重复定义并校验，只把请求触及的字段放入存储补丁。
func prepareRecurringUpdate(snapshot domain.TimeSnapshot, existing domain.Schedule, request UpdateScheduleRequest, candidate *domain.ScheduleUpdateCandidate) error {
	if snapshot.Location == nil {
		return domain.NewValidation("time location must not be nil", "location")
	}
	merged := existing
	if request.Cron != nil {
		rule, err := domain.ParseCron(*request.Cron)
		if err != nil {
			return err
		}
		merged.Cron = request.Cron
		candidate.Cron, candidate.CronPresent = request.Cron, true
		candidate.MonthEndPolicyPresent = true
		if rule.DayOfMonth != nil {
			policy := domain.MonthEndPolicyClamp
			merged.MonthEndPolicy = &policy
			candidate.MonthEndPolicy = &policy
		} else {
			merged.MonthEndPolicy = nil
			candidate.MonthEndPolicy = nil
		}
	}
	if request.UntilUnix != nil {
		date, err := recurringUntilDate(snapshot.Location, *request.UntilUnix)
		if err != nil {
			return err
		}
		merged.UntilDateLocal, candidate.UntilDateLocal = &date, &date
	}
	if merged.UntilDateLocal == nil {
		return domain.NewValidation("recurring cutoff is required", "until_unix")
	}
	if request.StartUnix != nil || request.EndUnix != nil {
		start, end, err := deriveUpdateEndpoints(existing, request.StartUnix, request.EndUnix)
		if err != nil {
			return err
		}
		validated, err := domain.ValidateRecurring(snapshot, start, end, *merged.Cron, *merged.UntilDateLocal)
		if err != nil {
			return err
		}
		merged.FirstStartLocal, merged.DurationMinutes = validated.FirstStartLocal, validated.DurationMinutes
		candidate.FirstStartLocal, candidate.DurationMinutes = &validated.FirstStartLocal, &validated.DurationMinutes
	}
	// 使用同一领域入口检查合并后的规则、日期和时长；零次发生也是合法有限定义。
	// 仅修改 cron 时不重写首次时间、时长或原始发生身份，避免历史重算改变定义。
	_, _, err := domain.NextBase(merged, snapshot.Now, snapshot.Location)
	return err
}

// recurringUntilDate 只在请求边界把瞬间转换为日历日期，避免缺失午夜的 DST 往返偏移。
func recurringUntilDate(location *time.Location, untilUnix int64) (string, error) {
	if location == nil {
		return "", domain.NewValidation("time location must not be nil", "location")
	}
	until := time.Unix(untilUnix, 0).In(location)
	if until.Year() < 1 || until.Year() > 9999 {
		return "", domain.NewValidation("cutoff date is out of range", "until_unix")
	}
	return until.Format(time.DateOnly), nil
}
