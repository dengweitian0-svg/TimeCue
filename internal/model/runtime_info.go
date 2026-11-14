package model

// RuntimeInfo 表示宿主本次启动公开的运行身份。
type RuntimeInfo struct {
	InstanceID   string   `json:"instanceId"`
	PID          int      `json:"pid"`
	APIVersion   string   `json:"apiVersion"`
	Capabilities []string `json:"capabilities"`
}
