package service

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// TestRecoveryCandidatesBoundariesAndContinuation 验证有界遍历与版本端点。
func TestRecoveryCandidatesBoundariesAndContinuation(t *testing.T) {
	start := time.Date(2020, 1, 1, 9, 0, 0, 0, model.BeijingLocation())
	v := model.ScheduleVersion{Schedule: model.Schedule{ID: 1, Name: "历史", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *", "0 9 * * *"}}}, From: start.Add(-time.Hour)}
	end := start.AddDate(0, 0, 501)
	first, after, done, err := PrepareRecoveryCandidates(context.Background(), v, start, end, time.Time{}, 500)
	if err != nil || done || len(first) != 500 || !first[0].NotifyTime.Equal(start) {
		t.Fatalf("首批错误: %d %v %v", len(first), done, err)
	}
	last, _, done, err := PrepareRecoveryCandidates(context.Background(), v, start, end, after, 500)
	if err != nil || !done || len(last) != 1 || !last[0].NotifyTime.Equal(start.AddDate(0, 0, 500)) {
		t.Fatalf("继续位置错误: %#v %v %v", last, done, err)
	}
	v.From = start
	until := start.AddDate(0, 0, 1)
	v.Until = &until
	values, _, done, err := PrepareRecoveryCandidates(context.Background(), v, start, end, time.Time{}, 500)
	if err != nil || !done || len(values) != 1 || !values[0].NotifyTime.Equal(until) {
		t.Fatal("版本必须严格晚于开始、不晚于结束", values, err)
	}
	v.Until = &v.From
	values, _, done, err = PrepareRecoveryCandidates(context.Background(), v, start, end, time.Time{}, 500)
	if err != nil || !done || len(values) != 0 {
		t.Fatal("零长度版本产生触发", values, err)
	}
	v.Until = nil
	v.Schedule.Period.Open = false
	values, _, done, err = PrepareRecoveryCandidates(context.Background(), v, start, end, time.Time{}, 500)
	if err != nil || !done || len(values) != 0 {
		t.Fatal("暂停版本产生遗漏", values, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := PrepareRecoveryCandidates(ctx, v, start, end, time.Time{}, 500); err == nil {
		t.Fatal("取消未传递")
	}
}
