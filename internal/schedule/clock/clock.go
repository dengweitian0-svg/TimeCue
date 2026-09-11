package clock

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// SystemClock 使用操作系统本地时钟提供时间快照。
type SystemClock struct{}

// Snapshot 返回当前时间和当前本地时区。
func (SystemClock) Snapshot(ctx context.Context) (domain.TimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.TimeSnapshot{}, err
	}
	// 返回的快照供一次请求复用，领域层不直接调用 time.Now()，从而保持规则可测试。
	now := time.Now()
	return domain.TimeSnapshot{Now: now, Location: time.Local}, nil
}
