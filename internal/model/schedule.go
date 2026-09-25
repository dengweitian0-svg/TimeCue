package model

import (
	"fmt"
	"strings"
	"time"
)

type ScheduleType string

const (
	SingleSchedule ScheduleType = "single"
	PeriodSchedule ScheduleType = "period"
)

// Schedule 保存生成未来提醒所需的规则。
// Cancelled 表示终止日程；Period.Open 控制可恢复的暂停。
type Schedule struct {
	ID          int
	Name        string
	Type        ScheduleType
	Description *string
	Period      *Period
	Single      *Single
	Deleted     bool // 删除后仍保留记录供历史查询。
	Cancelled   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Period 周期日程
type Period struct {
	Open bool     // 当前周期日程是否开放
	Cron []string // 支持多个日期
}

// Single 单次日程
type Single struct {
	NotifyTime time.Time
}

// Validate 检查日程结构；cron 语法由规则计算部分验证。
func (s Schedule) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("[model] schedule name must not be empty")
	}
	switch s.Type {
	case SingleSchedule:
		if s.Single == nil || s.Period != nil || s.Single.NotifyTime.IsZero() {
			return fmt.Errorf("[model] single schedule requires one notification time and no period rule")
		}
	case PeriodSchedule:
		if s.Period == nil || s.Single != nil || len(s.Period.Cron) == 0 {
			return fmt.Errorf("[model] period schedule requires cron rules and no single time")
		}
		for _, expression := range s.Period.Cron {
			if strings.TrimSpace(expression) == "" {
				return fmt.Errorf("[model] period schedule contains an empty cron rule")
			}
		}
	default:
		return fmt.Errorf("[model] unknown schedule type %q", s.Type)
	}
	return nil
}
