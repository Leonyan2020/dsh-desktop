package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"dsh-desktop/internal/singleinstance"
	"dsh-desktop/internal/tray"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if !singleinstance.Acquire() {
		return
	}

	app := NewApp()
	app.initApp()

	go tray.Start(nil, &tray.Callbacks{
		ShowWindow: func() {
			if app.ctx != nil {
				wailsruntime.WindowShow(app.ctx)
			}
		},
		HideWindow: func() {
			if app.ctx != nil {
				wailsruntime.WindowHide(app.ctx)
			}
		},
		Quit: func() { app.quit() },
		OnStart: func() error {
			return app.StartHarness()
		},
		OnStop: func() error {
			return app.StopHarness()
		},
		OnRestart: func() error {
			return app.RestartHarness()
		},
		OnEnterHarness: func() {
			_ = app.EnterHarness()
		},
		OnVersionManager: func() {
			app.ShowVersionManager()
		},
		StatusText: func() string {
			st := app.GetStatus()
			if st.Listening {
				if st.Version != "" {
					return "运行中 · " + st.Version
				}
				return "运行中"
			}
			if st.Running {
				return "启动中…"
			}
			return "已停止"
		},
		OnViewLog: func() {
			_ = app.OpenConsoleLog()
		},
	})

	singleinstance.ListenShowWindow(func() {
		if app.ctx != nil {
			wailsruntime.WindowShow(app.ctx)
		}
	})

	err := wails.Run(&options.App{
		Title:            "DSH Desktop",
		Width:            1280,
		Height:           860,
		MinWidth:         900,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 249, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			app.mu.Lock()
			q := app.quitting
			app.mu.Unlock()
			if q {
				return false
			}
			wailsruntime.WindowHide(ctx)
			return true
		},
		Bind: []interface{}{app},
		Windows: &windows.Options{
			WebviewGpuIsDisabled: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
