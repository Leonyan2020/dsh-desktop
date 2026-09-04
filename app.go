package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"dsh-desktop/internal/bundle"
	"dsh-desktop/internal/paths"
	"dsh-desktop/internal/process"
	"dsh-desktop/internal/rtmgr"
	"dsh-desktop/internal/tray"
)

// App is bound to the Wails frontend.
type App struct {
	ctx         context.Context
	bundle      *bundle.Manager
	rt          *rtmgr.Manager
	proc        *process.Manager
	mu          sync.Mutex
	quitting    bool
	pendingView string
	curTitle    string
	bootMu      sync.Mutex
}

func NewApp() *App {
	b := bundle.New()
	return &App{
		bundle:      b,
		rt:          rtmgr.New(b),
		proc:        process.New(),
		pendingView: "boot",
	}
}

func (a *App) initApp() {
	_ = paths.EnsureDirs()
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = paths.EnsureDirs()
}

func (a *App) shutdown(ctx context.Context) {
	_ = a.proc.Stop()
	tray.Quit()
}

func (a *App) quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	_ = a.proc.Stop()
	tray.Quit()
	if a.ctx != nil {
		wailsruntime.Quit(a.ctx)
	}
}

func (a *App) navigateInWindow(url string) {
	if a.ctx == nil || url == "" {
		return
	}
	js := fmt.Sprintf("window.location.replace(%q)", url)
	wailsruntime.WindowExecJS(a.ctx, js)
}

func (a *App) emitProgress(line string) {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "install-progress", line)
	}
}

// --- bound API ---

// setTitle 只在标题真正变化时才调用 WindowSetTitle。后者是一条同步的跨线程
// SendMessage（阻塞到 Wails 主线程处理完），而 GetStatus 会被托盘每 3 秒、
// 前端每 4 秒轮询，无条件改标题会让主线程持续被同步消息打断。
func (a *App) setTitle(title string) {
	if a.ctx == nil {
		return
	}
	a.mu.Lock()
	changed := a.curTitle != title
	a.curTitle = title
	a.mu.Unlock()
	if changed {
		wailsruntime.WindowSetTitle(a.ctx, title)
	}
}

func (a *App) GetStatus() process.Status {
	st := a.proc.Status()
	if st.Version == "" {
		ver, dir, err := a.rt.ActiveDir()
		if err == nil {
			st.Version = ver
			st.Runtime = dir
		} else {
			st.Message = err.Error()
		}
	}
	if st.Version != "" {
		a.setTitle("Harness " + st.Version)
	} else {
		a.setTitle("DSH Desktop")
	}
	return st
}

func (a *App) GetReadyStatus() rtmgr.ReadyStatus {
	return a.rt.ReadyStatus()
}

func (a *App) ConsumePendingView() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.pendingView
	if v == "" {
		return "boot"
	}
	a.pendingView = "boot"
	return v
}

// BootstrapEnvironment prepares Node/pnpm/dsh for zero-setup machines.
func (a *App) BootstrapEnvironment() error {
	// 托盘回调现在并行派发，托盘和前端可能同时触发引导；用 bootMu 串行化，
	// 等待方复查后直接返回，避免重复下载安装。
	a.bootMu.Lock()
	defer a.bootMu.Unlock()
	if ready := a.rt.ReadyStatus(); !ready.NeedsSetup && ready.NodeOK {
		return nil
	}
	err := a.rt.Bootstrap(func(line string) {
		a.emitProgress(line)
	})
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "ready-status", a.GetReadyStatus())
		wailsruntime.EventsEmit(a.ctx, "versions-changed", true)
	}
	return err
}

// waitReadyWithRetry waits for the port, and if the dsh process died before
// listening, retries once — antivirus/indexer scans have been seen making
// files transiently unreadable right after big installs, killing the boot.
func (a *App) waitReadyWithRetry() bool {
	if a.proc.WaitReady(3 * time.Minute) {
		return true
	}
	if a.proc.Status().Running {
		return false // still warming up
	}
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "toast", "dsh 进程提前退出（可能是杀毒软件瞬时锁定文件），正在自动重试…")
	}
	if err := a.proc.Restart(a.rt); err != nil {
		return false
	}
	return a.proc.WaitReady(3 * time.Minute)
}

func (a *App) StartHarness() error {
	ready := a.rt.ReadyStatus()
	if ready.NeedsSetup || !ready.NodeOK {
		if err := a.BootstrapEnvironment(); err != nil {
			return err
		}
	}
	if err := a.proc.Start(a.rt); err != nil {
		return err
	}
	go func() {
		ok := a.waitReadyWithRetry()
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, "status", a.GetStatus())
			if ok {
				wailsruntime.EventsEmit(a.ctx, "harness-ready", a.GetStatus())
			} else if !a.proc.Status().Running {
				// dsh 进程已经退出：把日志尾部的真实原因带出来
				msg := "dsh 启动失败，进程已退出"
				if tail := tailConsoleLog(8); tail != "" {
					msg += "：\n" + tail
				}
				wailsruntime.EventsEmit(a.ctx, "toast", msg)
			} else {
				wailsruntime.EventsEmit(a.ctx, "toast", "启动超时：插件热身可能仍在进行，可稍后点「进入 Harness」")
			}
		}
	}()
	return nil
}

// tailConsoleLog returns up to n trailing lines of the dsh console log,
// collapsed to a toast-friendly length.
func tailConsoleLog(n int) string {
	b, err := os.ReadFile(paths.ConsoleLog())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(out) > 600 {
		out = out[:600] + "…"
	}
	return out
}

func (a *App) StopHarness() error {
	return a.proc.Stop()
}

func (a *App) RestartHarness() error {
	if err := a.proc.Restart(a.rt); err != nil {
		return err
	}
	go func() {
		ok := a.waitReadyWithRetry()
		if a.ctx != nil {
			st := a.GetStatus()
			wailsruntime.EventsEmit(a.ctx, "status", st)
			if ok {
				wailsruntime.EventsEmit(a.ctx, "harness-ready", st)
			}
		}
	}()
	return nil
}

func (a *App) EnterHarness() error {
	ready := a.rt.ReadyStatus()
	if ready.NeedsSetup || !ready.NodeOK {
		if err := a.BootstrapEnvironment(); err != nil {
			return err
		}
	}
	st := a.GetStatus()
	if !st.Listening {
		if err := a.proc.Start(a.rt); err != nil {
			return err
		}
		if !a.waitReadyWithRetry() {
			st = a.GetStatus()
			if !st.Listening {
				return fmt.Errorf("dsh web 尚未在端口 %d 监听（冷启动约 1–3 分钟）", st.Port)
			}
		}
		st = a.GetStatus()
	}
	if a.ctx != nil {
		wailsruntime.WindowShow(a.ctx)
	}
	a.navigateInWindow(st.URL)
	return nil
}

func (a *App) ShowVersionManager() {
	a.mu.Lock()
	a.pendingView = "manage"
	a.mu.Unlock()
	if a.ctx != nil {
		wailsruntime.WindowShow(a.ctx)
		wailsruntime.WindowReloadApp(a.ctx)
	}
}

func (a *App) OpenHarnessUI() error {
	return a.EnterHarness()
}

func (a *App) OpenConsoleLog() error {
	p := paths.ConsoleLog()
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("日志不存在: %s", p)
	}
	// notepad 是 GUI 程序：HideWindow 会把它的主窗口一并隐藏，
	// 表现为「点了日志按钮没有任何反应」。
	cmd := exec.Command("notepad.exe", p)
	return cmd.Start()
}

func (a *App) ListLocalVersions() ([]rtmgr.VersionInfo, error) {
	return a.rt.ListLocal()
}

func (a *App) DiscoverVersions() ([]rtmgr.VersionInfo, error) {
	return a.rt.DiscoverRemote()
}

func (a *App) InstallVersion(version string, switchAfter bool) error {
	if _, err := a.bundle.EnsureReady(a.emitProgress); err != nil {
		return err
	}
	err := a.rt.Install(version, false, a.emitProgress)
	if err != nil {
		return err
	}
	if switchAfter {
		return a.SwitchVersion(version)
	}
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "versions-changed", true)
	}
	return nil
}

func (a *App) SwitchVersion(version string) error {
	prev, _, _ := a.rt.ActiveDir()
	if err := a.rt.SetActive(version); err != nil {
		return err
	}
	if err := a.proc.Restart(a.rt); err != nil {
		if prev != "" {
			_ = a.rt.SetActive(prev)
		}
		return fmt.Errorf("切换后启动失败，已回滚到 %s: %w", prev, err)
	}
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "versions-changed", true)
		wailsruntime.EventsEmit(a.ctx, "status", a.GetStatus())
	}
	return nil
}

func (a *App) GetAppInfo() map[string]string {
	return map[string]string{
		"name":    "DSH Desktop",
		"version": "0.2.3",
		"dshHome": paths.Home(),
		"note":    "安装包内附 Node20 + pnpm + 预置 Harness；首次可离线展开",
	}
}
