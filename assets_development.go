//go:build !production

package main

import (
	"embed"
	"io/fs"
)

// developmentFrontend 为测试、绑定生成和 Wails 开发服务器提供无构建副作用的入口占位资源。
//
//go:embed frontend/index.html
var developmentFrontend embed.FS

// frontendAssets 返回开发期资源文件系统；wails dev 的 Vite 服务会接管实际页面资源。
func frontendAssets() fs.FS {
	assets, err := fs.Sub(developmentFrontend, "frontend")
	if err != nil {
		panic(err)
	}
	return assets
}
