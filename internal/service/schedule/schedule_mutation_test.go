package schedule

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// TestSingleMutationSummary 验证单次改时与说明内容比较，不将敏感文本带入摘要。
func TestSingleMutationSummary(t *testing.T) {
	at := time.Date(2030, 10, 8, 13, 0, 0, 0, model.BeijingLocation())
	oldDescription, sameDescription, newDescription := "private-description", "private-description", "private-new-description"
	before := &model.Schedule{Name: "private-name", Description: &oldDescription, Type: model.SingleSchedule, Revision: 1, Single: &model.Single{NotifyTime: at}}
	for _, tc := range []struct {
		name        string
		description *string
		at          time.Time
		fields      []string
	}{
		{"same content and instant", &sameDescription, at.UTC(), []string{}},
		{"description cleared", nil, at, []string{"description"}},
		{"description and time changed", &newDescription, at.Add(time.Hour), []string{"description", "single.notify_time"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := *before
			after.Description = tc.description
			after.Single = &model.Single{NotifyTime: tc.at}
			after.Revision = 2
			summary := SummarizeScheduleWrite(&model.ScheduleWrite{Before: before, Schedule: &after, EffectiveAt: at.Add(-time.Hour)})
			if !reflect.DeepEqual(summary.ChangedFields, tc.fields) {
				t.Fatalf("实际差异错误: %#v", summary)
			}
			if change, ok := summary.Changes["single.notify_time"]; ok && (change.Before != "2030-10-08T13:00:00+08:00" || change.After != "2030-10-08T14:00:00+08:00") {
				t.Fatalf("时间差异错误: %#v", change)
			}
			encoded, err := json.Marshal(summary)
			if err != nil || strings.Contains(string(encoded), "private-") {
				t.Fatalf("文本进入受控视图: %s, err=%v", encoded, err)
			}
		})
	}
}
