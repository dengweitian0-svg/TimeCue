package model

import (
	"fmt"
	"time"
)

var beijingLocation = time.FixedZone("UTC+8", 8*60*60)

// BeijingLocation 返回固定的北京时间时区，偏移为 UTC+8，不采用夏令时。
func BeijingLocation() *time.Location {
	return beijingLocation
}

// ValidateUnixNanoTime 校验时间可无损存入 int64 Unix 纳秒字段。
// Go 的 UnixNano 仅在 1677-09-21 至 2262-04-11 范围内有定义。
func ValidateUnixNanoTime(value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("time must not be zero")
	}
	if value.Before(time.Unix(0, -1<<63)) || value.After(time.Unix(0, 1<<63-1)) {
		return fmt.Errorf("time is outside the Unix nanosecond range")
	}
	return nil
}
