package tools

import (
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// parseScheduleTime 只解析明确的分钟时间；相对日期在本次解析时读取北京时间。
func parseScheduleTime(raw string, now func() time.Time) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if at, err := time.Parse(time.RFC3339, text); err == nil {
		return at.In(model.BeijingLocation()), nil
	}
	parts := strings.Fields(text)
	if len(parts) == 2 {
		if days, ok := map[string]int{"今天": 0, "明天": 1, "后天": 2, "昨天": -1}[parts[0]]; ok {
			parts[0] = now().In(model.BeijingLocation()).AddDate(0, 0, days).Format("2006-01-02")
		}
		text = strings.Join(parts, " ")
		if at, err := time.ParseInLocation("2006-01-02 15:04", text, model.BeijingLocation()); err == nil && at.Format("2006-01-02 15:04") == text {
			return at, nil
		}
	}
	return time.Time{}, validation("notifyTime 需要完整日期与分钟时刻：YYYY-MM-DD HH:mm、今天/明天/后天/昨天 HH:mm 或 RFC3339；不明确请先澄清")
}
