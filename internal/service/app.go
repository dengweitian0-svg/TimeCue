package service

import "github.com/dengweitian0-svg/TimeCue/internal/config"

// App 统筹所有服务
type App struct {
	Schedule ScheduleRepo
	Notify   NotifyRepo
	Config   *config.Manager
}

// NewApp 预留应用组件的组装入口，具体生命周期仍待确定。
func NewApp(config *config.Manager, schedule ScheduleRepo, notify NotifyRepo) *App {
	return nil
}
