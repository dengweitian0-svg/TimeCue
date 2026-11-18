package agent

import "time"

// RetryDelay 返回当前重试序号的指数退避间隔。
func RetryDelay(retry int) time.Duration {
	delay := 5 * time.Second
	for i := 1; i < retry; i++ {
		// 防止极大配置造成 duration 溢出后退避变为负数。
		if delay > time.Duration(1<<62-1) {
			return time.Duration(1<<63 - 1)
		}
		delay *= 2
	}
	return delay
}

// RetryAt 返回可写入 SQLite 纳秒时间列的退避截止时刻。
func RetryAt(now time.Time, retry int) time.Time {
	next := now.Add(RetryDelay(retry))
	if latest := time.Unix(0, 1<<63-1); next.After(latest) {
		return latest
	}
	return next
}
