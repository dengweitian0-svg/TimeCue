package app

import (
	"context"
	"net/http"
	"os"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
)

// RuntimeDatabase 是运行时使用的数据库生命周期端口。
type RuntimeDatabase interface {
	Connect(context.Context, string) error
	CheckHealth(context.Context) error
	Close() error
}

// RuntimeOptions 注入外部适配器；路由由入口装配。
type RuntimeOptions struct {
	Database        RuntimeDatabase
	Handler         http.Handler
	Log             *logger.Logger
	Port            int
	OnReady         func() error
	PrepareDatabase func(context.Context) error
}

// RuntimeInfo 表示本次启动公开的运行身份。
type RuntimeInfo struct {
	InstanceID   string   `json:"instanceId"`
	PID          int      `json:"pid"`
	APIVersion   string   `json:"apiVersion"`
	Capabilities []string `json:"capabilities"`
}

// NewRuntimeInfo 构造当前进程的公开运行身份信息。
func NewRuntimeInfo(instanceID string) RuntimeInfo {
	return RuntimeInfo{InstanceID: instanceID, PID: os.Getpid(), APIVersion: "v1", Capabilities: []string{"delivery-v1", "recovery-v1", "task-members-v1", "managed-lifecycle-v1"}}
}
