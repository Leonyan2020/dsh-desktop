package process

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"dsh-desktop/internal/bundle"
	"dsh-desktop/internal/paths"
	"dsh-desktop/internal/rtmgr"
)

const DefaultPort = 3080

// dsh web 每次启动都轮换 launch token，只有先打开打印出来的
// "dsh web: <url>?token=…" 才会种下会话 cookie；裸地址会拿到
// 401 "authentication required; reopen the URL printed by dsh web"。
var webURLLine = regexp.MustCompile(`dsh web:\s*(\S+)`)

func parseWebURLLine(line string) string {
	line = strings.TrimRight(line, "\r")
	m := webURLLine.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	u := m[1]
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	return u
}

// consoleLogPath 测试可替换；默认指向用户目录下的 dsh 控制台日志。
var consoleLogPath = paths.ConsoleLog()

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
	mu        sync.Mutex
	cmd       *os.Process
	port      int
	version   string
	runtime   string
	webURL    string    // 本次启动打印的带 token URL；空表示尚未拿到
	logScanAt time.Time // 上次兜底扫描控制台日志的时间
	logFile   *os.File
	job       uintptr // Windows job handle
}

// teeWriter 把 dsh 输出转发进控制台日志，同时逐行扫描认证 URL。
// stdout/stderr 共用同一个 writer 时 os/exec 只建一条管道，写入是串行的。
type teeWriter struct {
	file *os.File
	mgr  *Manager
	buf  []byte
}

func (w *teeWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if u := parseWebURLLine(line); u != "" {
			w.mgr.mu.Lock()
			if w.mgr.webURL != u {
				w.mgr.webURL = u
				paths.AppendShellLog("captured web URL from stdout: %s", u)
			}
			w.mgr.mu.Unlock()
		}
	}
	if len(w.buf) > 64*1024 {
		w.buf = w.buf[:0] // 不成行的超长输出直接丢弃，防止内存膨胀
	}
	_, err := w.file.Write(p)
	return n, err
}

// healWebURLFromLog 兜底：stdout 没抓到认证 URL 时，从控制台日志尾部找
// 最后一行 "dsh web: <url>"。覆盖 teeWriter 漏采、以及壳重启后接管仍在
// 运行的 dsh（token 依旧有效）等情况。调用方需持有 m.mu。
func (m *Manager) healWebURLFromLog() {
	if m.webURL != "" || time.Since(m.logScanAt) < 2*time.Second {
		return
	}
	m.logScanAt = time.Now()
	f, err := os.Open(consoleLogPath)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	const tailN = 256 * 1024
	off := int64(0)
	if st.Size() > tailN {
		off = st.Size() - tailN
	}
	b := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil {
		return
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:] // 丢弃截断的首行
		}
	}
	for i := len(b) - 1; i >= 0; i-- { // 从尾部找最后一个换行，倒序逐行匹配
		if b[i] != '\n' {
			continue
		}
		if u := parseWebURLLine(string(b[i+1:])); u != "" {
			m.webURL = u
			paths.AppendShellLog("healed web URL from console log: %s", u)
			return
		}
	}
	// 整段没有换行时当作单行尝试
	if u := parseWebURLLine(string(b)); u != "" {
		m.webURL = u
		paths.AppendShellLog("healed web URL from console log: %s", u)
	}
}

// WaitWebURL waits until the running dsh has announced its authenticated URL.
// 端口先就绪、URL 行要等插件装载完才打印（实测可晚 1 分钟以上），进 Harness
// 前需要等它一下。
func (m *Manager) WaitWebURL(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		m.mu.Lock()
		m.healWebURLFromLog()
		u := m.webURL
		m.mu.Unlock()
		if u != "" {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// OwnsProcess reports whether the managed process was spawned by this shell.
// 外部 dsh web 的认证 URL 打印在别人终端里，永远到不了这里，等它没有意义。
func (m *Manager) OwnsProcess() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// PID returns the managed process id, or 0 when none is managed.
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return 0
	}
	return m.cmd.Pid
}

func New() *Manager {
	return &Manager{port: DefaultPort}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healWebURLFromLog()
	url := fmt.Sprintf("http://127.0.0.1:%d", m.port)
	if m.webURL != "" {
		url = m.webURL
	}
	st := Status{
		Port:    m.port,
		Version: m.version,
		Runtime: m.runtime,
		URL:     url,
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
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// 新进程会生成新 token：先丢弃旧 URL，再从输出里捕获本次的。
	m.webURL = ""
	tw := &teeWriter{file: f, mgr: m}
	cmd.Stdout = tw
	cmd.Stderr = tw

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return fmt.Errorf("启动 dsh web 失败: %w", err)
	}
	paths.AppendShellLog("spawned dsh pid=%d version=%s runtime=%s", cmd.Process.Pid, ver, dir)

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
	m.webURL = ""
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
