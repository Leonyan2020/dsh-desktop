package tray

import (
	"context"
	"runtime"
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
	// lastStatus caches the last rendered status line so the 3s poll skips
	// menu updates when nothing changed.
	lastStatus string

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

	// systray 在当前 OS 线程上创建隐藏窗口，而 Win32 要求该线程持续泵消息。
	// 必须锁住线程：否则系统繁忙时 Go 可能把本 goroutine 调度到别的线程，
	// 旧线程上的消息队列从此无人处理，托盘点击就再也弹不出来。
	// 因此 Start 必须像现在这样在独立 goroutine 中调用（main.go 里的 go tray.Start）。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
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

// handleMenu 必须永远不被回调阻塞：systray 投递点击是非阻塞的
// （忙时直接丢弃而不是排队），一旦某个回调在这里同步执行，
// 执行期间用户点的所有菜单项都会被静默吞掉。所以每个回调都派发到独立 goroutine。
// 并发重复操作由下层防护：proc.Start 有端口/cmd 双重检查，Bootstrap 有 bootMu。
func (t *Tray) handleMenu() {
	for {
		select {
		case <-t.mShow.ClickedCh:
			if t.cb.ShowWindow != nil {
				go t.cb.ShowWindow()
			}
		case <-t.mEnter.ClickedCh:
			if t.cb.OnEnterHarness != nil {
				go t.cb.OnEnterHarness()
			}
		case <-t.mVersions.ClickedCh:
			if t.cb.OnVersionManager != nil {
				go t.cb.OnVersionManager()
			}
		case <-t.mStart.ClickedCh:
			if t.cb.OnStart != nil {
				go func() { _ = t.cb.OnStart() }()
			}
		case <-t.mStop.ClickedCh:
			if t.cb.OnStop != nil {
				go func() { _ = t.cb.OnStop() }()
			}
		case <-t.mRestart.ClickedCh:
			if t.cb.OnRestart != nil {
				go func() { _ = t.cb.OnRestart() }()
			}
		case <-t.mLog.ClickedCh:
			if t.cb.OnViewLog != nil {
				go t.cb.OnViewLog()
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
	text := "状态: " + t.cb.StatusText()
	t.mu.Lock()
	unchanged := text == t.lastStatus
	t.lastStatus = text
	t.mu.Unlock()
	if unchanged {
		return
	}
	// SetTitle 会从外部线程调 SetMenuItemInfo；菜单正打开时这类跨线程菜单
	// API 会阻塞或干扰显示，所以只在文本真正变化时才调用。
	t.mStatus.SetTitle(text)
}
