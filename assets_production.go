//go:build production

package main

import (
	"embed"
	"io/fs"
)

// productionFrontend 嵌入 Wails 构建流程生成的完整生产资源。
//
//go:embed all:frontend/dist
var productionFrontend embed.FS

// frontendAssets 返回以 dist 为根的生产资源文件系统。
func frontendAssets() fs.FS {
	assets, err := fs.Sub(productionFrontend, "frontend/dist")
	if err != nil {
		panic(err)
	}
	return assets
}
