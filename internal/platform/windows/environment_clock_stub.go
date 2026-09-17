//go:build !windows

package windows

import "errors"

// NewEnvironmentClock 报告非 Windows 平台不提供系统环境消息线程。
func NewEnvironmentClock() (*EnvironmentClock, error) {
	return nil, errors.New("create environment clock: Windows is required")
}

// NewEnvironmentClockWithMessageThread 在非 Windows 平台明确返回不支持错误。
func NewEnvironmentClockWithMessageThread(*MessageThread) (*EnvironmentClock, error) {
	return nil, errors.New("create environment clock: Windows is required")
}
