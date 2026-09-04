package tray

import (
	"context"
	"sync"
	"time"

	"github.com/getlantern/systray"
)

type Callbacks struct {
	ShowWindow       func()
	HideWindow       func()
	Quit             func()
	OnStart          func() error
	OnStop           func() error
	OnRestart        func() error
	OnEnterHarness   func()
	OnVersionManager func()
	StatusText       func() string
	OnViewLog        func()
}

type Tray struct {
	cb  *Callbacks
	ctx context.Context
	mu  sync.Mutex

	mShow     *systray.MenuItem
	mEnter    *systray.MenuItem
	mVersions *systray.MenuItem
	mStatus   *systray.MenuItem
	mStart    *systray.MenuItem
	mStop     *systray.MenuItem
	mRestart  *systray.MenuItem
	mLog      *systray.MenuItem
	mQuit     *systray.MenuItem
}

var (
	instance   *Tray
	instanceMu sync.Mutex
)

func Start(ctx context.Context, cb *Callbacks) {
	instanceMu.Lock()
	instance = &Tray{cb: cb, ctx: ctx}
	instanceMu.Unlock()
	systray.Run(onReady, onExit)
}

func Quit() { systray.Quit() }

func onReady() {
	systray.SetIcon(iconBytes)
	systray.SetTitle("DSH Desktop")
	systray.SetTooltip("DeepSeek Harness 桌面端")

	t := getInstance()
	if t == nil {
		return
	}

	t.mShow = systray.AddMenuItem("显示主窗口", "")
	t.mEnter = systray.AddMenuItem("进入 Harness", "")
	t.mVersions = systray.AddMenuItem("版本管理", "")
	systray.AddSeparator()
	t.mStatus = systray.AddMenuItem("状态: …", "")
	t.mStatus.Disable()
	t.mStart = systray.AddMenuItem("启动 dsh web", "")
	t.mStop = systray.AddMenuItem("停止 dsh web", "")
	t.mRestart = systray.AddMenuItem("重启 dsh web", "")
	t.mLog = systray.AddMenuItem("查看控制台日志", "")
	systray.AddSeparator()
	t.mQuit = systray.AddMenuItem("退出", "")

	go t.handleMenu()
	go t.pollStatus()
}

func onExit() {}

func getInstance() *Tray {
	instanceMu.Lock()
	defer instanceMu.Unlock()
	return instance
}

func (t *Tray) handleMenu() {
	for {
		select {
		case <-t.mShow.ClickedCh:
			if t.cb.ShowWindow != nil {
				t.cb.ShowWindow()
			}
		case <-t.mEnter.ClickedCh:
			if t.cb.OnEnterHarness != nil {
				t.cb.OnEnterHarness()
			}
		case <-t.mVersions.ClickedCh:
			if t.cb.OnVersionManager != nil {
				t.cb.OnVersionManager()
			}
		case <-t.mStart.ClickedCh:
			if t.cb.OnStart != nil {
				_ = t.cb.OnStart()
			}
		case <-t.mStop.ClickedCh:
			if t.cb.OnStop != nil {
				_ = t.cb.OnStop()
			}
		case <-t.mRestart.ClickedCh:
			if t.cb.OnRestart != nil {
				_ = t.cb.OnRestart()
			}
		case <-t.mLog.ClickedCh:
			if t.cb.OnViewLog != nil {
				t.cb.OnViewLog()
			}
		case <-t.mQuit.ClickedCh:
			if t.cb.Quit != nil {
				go t.cb.Quit()
			}
		}
	}
}

func (t *Tray) pollStatus() {
	for {
		t.updateStatus()
		time.Sleep(3 * time.Second)
	}
}

func (t *Tray) updateStatus() {
	if t.cb.StatusText == nil || t.mStatus == nil {
		return
	}
	t.mStatus.SetTitle("状态: " + t.cb.StatusText())
}
