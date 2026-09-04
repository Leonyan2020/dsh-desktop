package bundle

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"dsh-desktop/internal/paths"
)

// Pinned portable toolchain versions for offline-friendly installs.
const (
	// Node 22 LTS: dsh 运行时用到 Promise.withResolvers(22.0)、
	// module.stripTypeScriptTypes(22.12)、zlib zstd(22.15)，Node 20 无法启动。
	NodeVersion = "22.23.2"
	// MinNodeSemver 是 dsh 运行时能启动的最低 Node 版本，低于它时自动重新下载。
	MinNodeSemver = "22.15.0"
	PnpmVersion   = "10.34.5"
	DefaultDSH    = "" // empty = resolve npm dist-tag "latest" at bootstrap time
)

type ProgressFunc func(line string)

type Tools struct {
	Node string
	Pnpm string
}

type Manager struct {
	mu    sync.Mutex
	tools Tools
}

func New() *Manager { return &Manager{} }

func (m *Manager) Get() Tools {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools
}

// Probe returns whatever toolchain is currently available (may be partial).
func (m *Manager) Probe() Tools {
	node := findNode()
	pnpm := findPnpm(node)
	t := Tools{Node: node, Pnpm: pnpm}
	m.mu.Lock()
	if node != "" && pnpm != "" {
		m.tools = t
	}
	m.mu.Unlock()
	return t
}

// Resolve finds bundled / cached / PATH tools without downloading.
// Both Node and pnpm must be present.
func (m *Manager) Resolve() (Tools, error) {
	t := m.Probe()
	if t.Node == "" {
		return t, fmt.Errorf("未找到 Node：请使用安装包，或首次联网自动下载")
	}
	if t.Pnpm == "" {
		return t, fmt.Errorf("未找到 pnpm：将在首次引导时自动安装")
	}
	return t, nil
}

// EnsureReady downloads Node/pnpm if needed. Safe to call repeatedly.
func (m *Manager) EnsureReady(progress ProgressFunc) (Tools, error) {
	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	if err := paths.EnsureDirs(); err != nil {
		return Tools{}, err
	}

	node := findNode()
	if node != "" && !NodeSatisfiesPath(node) {
		report(fmt.Sprintf("当前 Node 过旧（%s < v%s，dsh 无法启动），正在下载 Node.js %s …\n", versionLabel(node), MinNodeSemver, NodeVersion))
		if err := downloadNode(progress); err != nil {
			return Tools{}, err
		}
		node = findNode()
	}
	if node == "" {
		report(fmt.Sprintf("正在下载 Node.js %s …\n", NodeVersion))
		if err := downloadNode(progress); err != nil {
			return Tools{}, err
		}
		node = findNode()
		if node == "" {
			return Tools{}, fmt.Errorf("Node 下载完成但仍未找到 node.exe")
		}
	}
	if !NodeSatisfiesPath(node) {
		return Tools{}, fmt.Errorf("Node 仍过旧（%s < v%s）：请删除 %%LOCALAPPDATA%%\\dsh-desktop\\runtime 后重试", versionLabel(node), MinNodeSemver)
	}
	report("Node 就绪: " + versionLabel(node) + " (" + node + ")\n")

	pnpm := findPnpm(node)
	if pnpm == "" {
		report(fmt.Sprintf("正在安装 pnpm %s …\n", PnpmVersion))
		if err := downloadPnpm(progress); err != nil {
			return Tools{}, err
		}
		pnpm = findPnpm(node)
		if pnpm == "" {
			return Tools{}, fmt.Errorf("pnpm 安装失败")
		}
		report("pnpm 就绪: " + pnpm + "\n")
	} else {
		report("已找到 pnpm: " + pnpm + "\n")
	}

	m.mu.Lock()
	m.tools = Tools{Node: node, Pnpm: pnpm}
	t := m.tools
	m.mu.Unlock()
	return t, nil
}

func findNode() string {
	candidates := []string{
		filepath.Join(paths.ExeDir(), "runtime", "node", "node.exe"),
		filepath.Join(paths.AppNodeDir(), "node.exe"),
	}
	// Prefer a candidate that can actually run dsh; keep the first (too-old)
	// one only as a last-resort fallback so callers can surface a clear error.
	fallback := ""
	for _, c := range candidates {
		if !fileExists(c) {
			continue
		}
		if NodeSatisfiesPath(c) {
			return c
		}
		if fallback == "" {
			fallback = c
		}
	}
	if p, err := exec.LookPath("node"); err == nil {
		if NodeSatisfiesPath(p) {
			return p
		}
		if fallback == "" {
			fallback = p
		}
	}
	return fallback
}

// nodeSemver is the parsed "vMAJOR.MINOR.PATCH" of a node binary, cached per
// path because Probe() runs on every UI status poll.
type nodeSemver struct {
	major, minor, patch int
	ok                  bool
}

var nodeSemverCache sync.Map

// NodeSatisfiesPath reports whether the node binary at path is at least
// MinNodeSemver.
func NodeSatisfiesPath(nodePath string) bool {
	if nodePath == "" {
		return false
	}
	if v, ok := nodeSemverCache.Load(nodePath); ok {
		s, _ := v.(nodeSemver)
		return s.ok && semverAtLeast(s, MinNodeSemver)
	}
	s := probeNodeSemver(nodePath)
	nodeSemverCache.Store(nodePath, s)
	return s.ok && semverAtLeast(s, MinNodeSemver)
}

func probeNodeSemver(nodePath string) nodeSemver {
	cmd := exec.Command(nodePath, "-v")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nodeSemver{}
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "v%d.%d.%d", &major, &minor, &patch); err != nil {
		return nodeSemver{}
	}
	return nodeSemver{major: major, minor: minor, patch: patch, ok: true}
}

func semverAtLeast(s nodeSemver, min string) bool {
	var mj, mn, pt int
	if _, err := fmt.Sscanf(min, "%d.%d.%d", &mj, &mn, &pt); err != nil {
		return true
	}
	if s.major != mj {
		return s.major > mj
	}
	if s.minor != mn {
		return s.minor > mn
	}
	return s.patch >= pt
}

// versionLabel renders a node binary's "vMAJOR.MINOR.PATCH", falling back to
// the path when the version cannot be probed.
func versionLabel(nodePath string) string {
	if v, ok := nodeSemverCache.Load(nodePath); ok {
		s, _ := v.(nodeSemver)
		if s.ok {
			return fmt.Sprintf("v%d.%d.%d", s.major, s.minor, s.patch)
		}
	}
	return nodePath
}

func findPnpm(node string) string {
	candidates := []string{
		filepath.Join(paths.ExeDir(), "runtime", "pnpm.exe"),
		paths.AppPnpmPath(),
		filepath.Join(filepath.Dir(node), "pnpm.cmd"),
		filepath.Join(filepath.Dir(node), "pnpm.exe"),
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	if p, err := exec.LookPath("pnpm"); err == nil {
		return p
	}
	return ""
}

func downloadNode(progress ProgressFunc) error {
	urls := []string{
		fmt.Sprintf("https://cdn.npmmirror.com/binaries/node/v%s/node-v%s-win-x64.zip", NodeVersion, NodeVersion),
		fmt.Sprintf("https://nodejs.org/dist/v%s/node-v%s-win-x64.zip", NodeVersion, NodeVersion),
	}
	zipPath := filepath.Join(paths.AppRuntimeDir(), "node.zip")
	destRoot := paths.AppNodeDir()
	_ = os.RemoveAll(destRoot)
	_ = os.MkdirAll(paths.AppRuntimeDir(), 0o755)

	var lastErr error
	for _, url := range urls {
		if err := downloadFile(url, zipPath, progress); err != nil {
			lastErr = err
			_ = os.Remove(zipPath)
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return lastErr
	}
	defer os.Remove(zipPath)

	if err := unzipFlatten(zipPath, destRoot, "node-v"+NodeVersion+"-win-x64"); err != nil {
		return err
	}
	if !fileExists(filepath.Join(destRoot, "node.exe")) {
		return fmt.Errorf("解压后缺少 node.exe")
	}
	return nil
}

func downloadPnpm(progress ProgressFunc) error {
	url := fmt.Sprintf("https://github.com/pnpm/pnpm/releases/download/v%s/pnpm-win-x64.exe", PnpmVersion)
	dest := paths.AppPnpmPath()
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	return downloadFile(url, dest, progress)
}

func downloadFile(url, dest string, progress ProgressFunc) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "dsh-desktop/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败 %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败 HTTP %d: %s", resp.StatusCode, url)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	written, err := io.Copy(f, resp.Body)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if progress != nil {
		progress(fmt.Sprintf("已下载 %.1f MB → %s\n", float64(written)/1024/1024, dest))
	}
	_ = os.Remove(dest)
	return os.Rename(tmp, dest)
}

// unzipFlatten extracts zip, stripping the top folder prefix if present.
func unzipFlatten(zipPath, dest, stripPrefix string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	_ = os.MkdirAll(dest, 0o755)
	prefix := stripPrefix + "/"
	for _, f := range r.File {
		name := f.Name
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("非法 zip 路径: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Cmd prepares a hidden console command with PATH including node dir.
func Cmd(tools Tools, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	nodeDir := filepath.Dir(tools.Node)
	path := os.Getenv("PATH")
	cmd.Env = append(os.Environ(), "PATH="+nodeDir+string(os.PathListSeparator)+path)
	return cmd
}
