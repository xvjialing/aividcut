package main

import (
	"embed"
	"encoding/json"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// 系统运行图标与打包图标共用源文件，避免启动后回到框架默认图标。
//
//go:embed build/appicon.png
var appIcon []byte

func main() {
	app, err := NewApp()
	if err != nil {
		log.Fatal(err)
	}
	desktop := application.New(application.Options{
		Name: "AIVidCut", Description: "AI 视频剪辑工作台 · 开发预览版本",
		Icon:         appIcon,
		Services:     []application.Service{application.NewService(app)},
		Assets:       application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		MarshalError: func(err error) []byte { value, _ := json.Marshal(err.Error()); return value },
		ShouldQuit:   func() bool { return !app.requestClose() },
		Mac:          application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	app.desktop = desktop
	app.window = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "editor", Title: "AIVidCut", URL: "/",
		Width: 1480, Height: 940, MinWidth: 1000, MinHeight: 700,
		BackgroundColour: application.NewRGB(20, 23, 28),
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
	})
	// 关闭按钮和应用退出共用草稿检查，不能绕过前端尚未提交的输入。
	app.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if app.requestClose() {
			event.Cancel()
		}
	})
	if err := desktop.Run(); err != nil {
		app.service.Close()
		log.Fatal(err)
	}
}
