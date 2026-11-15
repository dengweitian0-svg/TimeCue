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
