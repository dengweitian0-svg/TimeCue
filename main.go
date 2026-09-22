package main

import (
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
)

func main() {
	logger := logger.New()
	logger.Info("Starting TimeCue...")
	// 其他初始化和启动逻辑
	manager, err := config.NewManager()
	if err != nil {
		logger.Error("Failed to create config manager", "error", err)
		return
	}
	if err := manager.LoadConfig(); err != nil {
		logger.Error("Failed to load config", "error", err)
		return
	}
	_ = manager
}
