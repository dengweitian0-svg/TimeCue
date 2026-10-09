package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// WriteSchedule 将规则校验与下一次触发计算放进正式存储事务。
func (a *App) WriteSchedule(ctx context.Context, schedule model.Schedule, expectedRevision int, key string) (*model.ScheduleWrite, error) {
	if len(key) > 128 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n") {
		return nil, ErrInvalidSchedule
	}
	schedule.Name = strings.TrimSpace(schedule.Name)
	input, _ := json.Marshal(struct {
		Schedule model.Schedule
		Revision int
	}{schedule, expectedRevision})
	hash := sha256.Sum256(input)
	result, err := a.Schedule.ApplyScheduleChange(ctx, model.ScheduleChange{Schedule: schedule, ExpectedRevision: expectedRevision, Key: key, Digest: hex.EncodeToString(hash[:])}, func(current *model.Schedule, now time.Time) (*time.Time, error) {
		// 新版本的创建和更新均只接受分钟精度，不取整也不原样保存秒级规则。
		if schedule.Single != nil {
			if schedule.Single.NotifyTime.Second() != 0 || schedule.Single.NotifyTime.Nanosecond() != 0 {
				return nil, ErrInvalidSchedule
			}
		}
		if current == nil {
			if schedule.ID != 0 || schedule.Deleted || schedule.Cancelled {
				return nil, ErrInvalidSchedule
			}
		} else {
			if expectedRevision < 1 || current.Cancelled || current.Type != schedule.Type {
				return nil, ErrInvalidSchedule
			}
			if current.Single != nil && !current.Single.NotifyTime.After(now) &&
				(schedule.Single == nil || !current.Single.NotifyTime.Equal(schedule.Single.NotifyTime)) {
				return nil, ErrInvalidSchedule
			}
		}
		next, ok, e := nextNotificationTime(schedule, now)
		if e != nil {
			return nil, ErrInvalidSchedule
		}
		if schedule.Type == model.SingleSchedule && !ok && (current == nil || current.Single.NotifyTime.After(now)) {
			return nil, ErrInvalidSchedule
		}
		if !ok {
			return nil, nil
		}
		return &next, nil
	})
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	if err == nil && a.Scheduler != nil {
		a.Scheduler.ScheduleChanged(ctx, result)
	}
	return result, err
}
