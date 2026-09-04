package process

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"dsh-desktop/internal/bundle"
	"dsh-desktop/internal/paths"
	"dsh-desktop/internal/rtmgr"
)

const DefaultPort = 3080

// Status describes the managed dsh web process.
type Status struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	Version   string `json:"version"`
	Runtime   string `json:"runtime"`
	URL       string `json:"url"`
	Listening bool   `json:"listening"`
	Message   string `json:"message"`
}

// Manager starts/stops dsh web under a Windows Job Object.
type Manager struct {
	mu      sync.Mutex
	cmd     *os.Process
	port    int
	version string
	runtime string
	logFile *os.File
	job     uintptr // Windows job handle
}

func New() *Manager {
	return &Manager{port: DefaultPort}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		Port:    m.port,
		Version: m.version,
		Runtime: m.runtime,
		URL:     fmt.Sprintf("http://127.0.0.1:%d", m.port),
	}
	if m.cmd != nil {
		st.PID = m.cmd.Pid
		st.Running = true
	}
	st.Listening = portListening(m.port)
	if st.Listening && !st.Running {
		st.Message = "端口已被占用（可能是外部 dsh web）"
		st.Running = true // treat as up for UI
	}
	return st
}

// Start launches dsh web from the active runtime. If port already listening, no-op.
func (m *Manager) Start(rt *rtmgr.Manager) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	if portListening(m.port) {
		ver, dir, _ := rt.ActiveDir()
		m.version = ver
		m.runtime = dir
		return nil
	}
	if m.cmd != nil {
		return nil
	}

	ver, dir, err := rt.ActiveDir()
	if err != nil {
		return err
	}
	bin := rtmgr.DshBinary(dir)
	if bin == "" {
		return fmt.Errorf("运行时目录无 dsh: %s", dir)
	}

	logPath := paths.ConsoleLog()
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.Create(logPath) // truncate each start
	if err != nil {
		return err
	}
	m.logFile = f

	var cmd *exec.Cmd
	node := rt.NodePath()
	if node == "" {
		if p, err := exec.LookPath("node"); err == nil {
			node = p
		}
	}
	// dsh 运行时需要 Node ≥ bundle.MinNodeSemver；用旧 Node 启动会在插件树
	// 加载时直接崩溃（Promise.withResolvers / zlib zstd 缺失），先挡下来。
	if node != "" && !bundle.NodeSatisfiesPath(node) {
		_ = f.Close()
		return fmt.Errorf("Node 过旧（%s），dsh 需要 ≥ v%s：请点「重新准备环境」自动升级内置 Node", node, bundle.MinNodeSemver)
	}
	if filepath.Ext(bin) == ".js" {
		if node == "" {
			_ = f.Close()
			return fmt.Errorf("找不到 node（请先完成首次环境准备）")
		}
		// --no-open：dsh web 默认会拉起系统默认浏览器，桌面壳内嵌 WebView，
		// 不需要再弹浏览器页面。
		cmd = exec.Command(node, bin, "web", "--port", fmt.Sprintf("%d", m.port), "--no-open")
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"))
	} else if node != "" {
		cmd = exec.Command(bin, "web", "--port", fmt.Sprintf("%d", m.port), "--no-open")
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"))
	} else {
		cmd = exec.Command(bin, "web", "--port", fmt.Sprintf("%d", m.port), "--no-open")
	}
	cmd.Dir = dir
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return fmt.Errorf("启动 dsh web 失败: %w", err)
	}

	m.cmd = cmd.Process
	m.version = ver
	m.runtime = dir

	if err := assignToJob(cmd.Process.Pid, &m.job); err != nil {
		// non-fatal: log but keep running
		_, _ = fmt.Fprintf(f, "\n[desktop] Job Object assign failed: %v\n", err)
	}

	go func(p *os.Process, lf *os.File) {
		_, _ = p.Wait()
		m.mu.Lock()
		if m.cmd == p {
			m.cmd = nil
		}
		if m.logFile == lf {
			_ = lf.Close()
			m.logFile = nil
		}
		m.mu.Unlock()
	}(cmd.Process, f)

	return nil
}

// Stop kills the managed process (and job children). When this instance does
// not manage a process but the port is still held by an orphan dsh web from a
// crashed earlier instance, the orphan is cleaned up too — otherwise tray
// 停止/退出 would silently leave the service running.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return killPortOrphan(m.port)
	}
	pid := m.cmd.Pid
	_ = killTree(pid)
	m.cmd = nil
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}
	closeJob(&m.job)
	return nil
}

// Restart stops then starts.
func (m *Manager) Restart(rt *rtmgr.Manager) error {
	_ = m.Stop()
	// wait port free
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !portListening(m.port) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	return m.Start(rt)
}

// WaitReady polls until port listens or timeout.
func (m *Manager) WaitReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portListening(m.port) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func portListening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
