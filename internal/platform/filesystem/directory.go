// Package filesystem 提供平台无关的本地文件系统目录端口实现。
package filesystem

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
)

// DirectoryAccess 将配置层的目录验证规则接入 platform.DirectoryAccess 窄端口。
type DirectoryAccess struct{}

// ValidateDirectory 创建并验证目录后返回解析过物理别名的路径。
func (DirectoryAccess) ValidateDirectory(ctx context.Context, path string) (string, error) {
	return config.ValidateDirectoryPath(ctx, path)
}

var _ platform.DirectoryAccess = DirectoryAccess{}
