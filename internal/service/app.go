package service

import (
	"fmt"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

// App 统筹所有服务
type App struct {
	Schedule ScheduleRepo
	Notify   NotifyRepo
	Config   *config.Manager
}

// NewApp 使用依赖注入的配置与 Repo 接口组装应用服务。
func NewApp(manager *config.Manager, schedule ScheduleRepo, notify NotifyRepo) (*App, error) {
	if manager == nil {
		return nil, fmt.Errorf("[app] config manager is nil")
	}
	if schedule == nil {
		return nil, fmt.Errorf("[app] schedule repo is nil")
	}
	if notify == nil {
		return nil, fmt.Errorf("[app] notification repo is nil")
	}
	return &App{Schedule: schedule, Notify: notify, Config: manager}, nil
}
