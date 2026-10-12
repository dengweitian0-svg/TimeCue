package service

import (
	"slices"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ScheduleMutation 只暴露允许记录的规则事实，不包含名称和说明文本。
type ScheduleMutation struct {
	Outcome        string                         `json:"outcome"`
	RevisionBefore *int                           `json:"revision_before"`
	RevisionAfter  int                            `json:"revision_after"`
	EffectiveAt    string                         `json:"effective_at"`
	State          map[string]any                 `json:"state,omitempty"`
	ChangedFields  []string                       `json:"changed_fields"`
	Changes        map[string]ScheduleFieldChange `json:"changes,omitempty"`
	NotificationID *int                           `json:"notification_id"`
	NextNotifyAt   *string                        `json:"next_notify_at"`
}

// ScheduleFieldChange 保存公开规则值的实际差异。
type ScheduleFieldChange struct {
	Before any `json:"before"`
	After  any `json:"after"`
}

// SummarizeScheduleWrite 根据正式操作结果生成受控视图，不额外查询状态。
func SummarizeScheduleWrite(result *model.ScheduleWrite) ScheduleMutation {
	after := result.Schedule
	summary := ScheduleMutation{
		Outcome: "applied", RevisionAfter: after.Revision,
		EffectiveAt:   formatMutationTime(result.EffectiveAt),
		ChangedFields: []string{},
	}
	if result.Notification != nil {
		id := result.Notification.ID
		at := formatMutationTime(result.Notification.NotifyTime)
		summary.NotificationID = &id
		summary.NextNotifyAt = &at
	}
	// 重放只描述原回执，不能把原操作再次表达为本次变更。
	if result.Replayed {
		summary.Outcome = "replayed"
		return summary
	}
	before := result.Before
	if before == nil {
		summary.State = map[string]any{"type": after.Type}
		if after.Period != nil {
			summary.State["period.open"] = after.Period.Open
			summary.State["period.cron"] = slices.Clone(after.Period.Cron)
		}
		if after.Single != nil {
			summary.State["single.notify_time"] = formatMutationTime(after.Single.NotifyTime)
		}
		return summary
	}
	revision := before.Revision
	summary.RevisionBefore = &revision
	if before.Name != after.Name {
		summary.ChangedFields = append(summary.ChangedFields, "name")
	}
	if !equalDescription(before.Description, after.Description) {
		summary.ChangedFields = append(summary.ChangedFields, "description")
	}
	add := func(field string, old, new any) {
		summary.ChangedFields = append(summary.ChangedFields, field)
		if summary.Changes == nil {
			summary.Changes = make(map[string]ScheduleFieldChange)
		}
		summary.Changes[field] = ScheduleFieldChange{Before: old, After: new}
	}
	if before.Type != after.Type {
		add("type", before.Type, after.Type)
	}
	if before.Period != nil && after.Period != nil {
		if before.Period.Open != after.Period.Open {
			add("period.open", before.Period.Open, after.Period.Open)
		}
		if !slices.Equal(before.Period.Cron, after.Period.Cron) {
			add("period.cron", slices.Clone(before.Period.Cron), slices.Clone(after.Period.Cron))
		}
	}
	if before.Single != nil && after.Single != nil && !before.Single.NotifyTime.Equal(after.Single.NotifyTime) {
		add("single.notify_time", formatMutationTime(before.Single.NotifyTime), formatMutationTime(after.Single.NotifyTime))
	}
	if before.Deleted != after.Deleted {
		add("deleted", before.Deleted, after.Deleted)
	}
	if before.Cancelled != after.Cancelled {
		add("cancelled", before.Cancelled, after.Cancelled)
	}
	return summary
}

func equalDescription(before, after *string) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return *before == *after
}

func formatMutationTime(at time.Time) string {
	return at.In(model.BeijingLocation()).Format(time.RFC3339Nano)
}
