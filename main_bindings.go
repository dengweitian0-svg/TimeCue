//go:build bindings

package main

import (
	"fmt"
	"os"

	appRuntime "github.com/dengweitian0-svg/TimeCue/internal/runtime"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// main 仅向 Wails 生成器暴露绑定形状，不进入 bootstrap、实例锁、SQLite 或 scheduler。
func main() {
	err := wails.Run(&options.App{
		Title:       "TimeCue Bindings",
		AssetServer: &assetserver.Options{Assets: frontendAssets()},
		Bind:        []interface{}{appRuntime.NewWailsBindingsForGeneration()},
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
