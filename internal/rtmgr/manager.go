package rtmgr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"dsh-desktop/internal/bundle"
	"dsh-desktop/internal/paths"
)

const npmPackage = "@deepseek-ai/dsh"

// VersionInfo is one installable / installed release.
type VersionInfo struct {
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Legacy    bool   `json:"legacy"`
	Path      string `json:"path"`
	Tag       string `json:"tag"`
}

// ProgressFunc reports install progress lines to the UI.
type ProgressFunc func(line string)

// Manager discovers, installs, and switches @deepseek-ai/dsh versions.
type Manager struct {
	mu     sync.Mutex
	bundle *bundle.Manager
	tools  bundle.Tools
}

func New(b *bundle.Manager) *Manager {
	return &Manager{bundle: b}
}

func (m *Manager) resolveTools() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tools.Node != "" && m.tools.Pnpm != "" {
		return nil
	}
	t, err := m.bundle.Resolve()
	if err != nil {
		return err
	}
	m.tools = t
	return nil
}

func (m *Manager) setTools(t bundle.Tools) {
	m.mu.Lock()
	m.tools = t
	m.mu.Unlock()
}

// NodePath returns the resolved node.exe (empty if unresolved).
func (m *Manager) NodePath() string {
	_ = m.resolveTools()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools.Node
}

// ActiveVersion returns the currently selected version string (may be empty).
func (m *Manager) ActiveVersion() string {
	b, err := os.ReadFile(paths.ActiveFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ActiveDir returns the filesystem path of the active runtime, or legacy dsh-run.
func (m *Manager) ActiveDir() (string, string, error) {
	if err := paths.EnsureDirs(); err != nil {
		return "", "", err
	}
	ver := m.ActiveVersion()
	if ver != "" {
		dir := paths.RuntimeDir(ver)
		if hasDshBinary(dir) {
			return ver, dir, nil
		}
	}
	// No usable active pointer: adopt the newest managed runtime instead of
	// silently falling back to a legacy install that may be too old to boot.
	locals, err := m.ListLocal()
	if err == nil {
		for _, l := range locals {
			if l.Legacy {
				continue
			}
			if err := m.SetActive(l.Version); err == nil {
				return l.Version, l.Path, nil
			}
		}
	}
	legacy := paths.LegacyRun()
	if hasDshBinary(legacy) {
		v := readPackageVersion(legacy)
		return v, legacy, nil
	}
	return "", "", fmt.Errorf("尚未安装 dsh 运行时：首次启动会自动安装，或到「版本管理」手动安装")
}

// ListLocal returns installed runtimes (runtimes/* + optional legacy).
func (m *Manager) ListLocal() ([]VersionInfo, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	active := m.ActiveVersion()
	var out []VersionInfo

	entries, _ := os.ReadDir(paths.RuntimesDir())
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := paths.RuntimeDir(name)
		if !RuntimeReady(dir) {
			continue
		}
		v := readPackageVersion(dir)
		if v == "" {
			v = name
		}
		out = append(out, VersionInfo{
			Version:   v,
			Installed: true,
			Active:    v == active || name == active,
			Path:      dir,
			Tag:       classify(v),
		})
	}

	legacy := paths.LegacyRun()
	if hasDshBinary(legacy) {
		v := readPackageVersion(legacy)
		if v == "" {
			v = "legacy"
		}
		already := false
		for _, x := range out {
			if x.Version == v {
				already = true
				break
			}
		}
		if !already {
			out = append(out, VersionInfo{
				Version:   v,
				Installed: true,
				Active:    active == v || active == "",
				Legacy:    true,
				Path:      legacy,
				Tag:       classify(v),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Version > out[j].Version
	})
	return out, nil
}

// DiscoverRemote lists versions published on npm (newest first).
func (m *Manager) DiscoverRemote() ([]VersionInfo, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	url := "https://registry.npmjs.org/" + npmPackage
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("npm registry unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("npm registry HTTP %d: %s", resp.StatusCode, string(body))
	}

	var meta struct {
		Versions map[string]json.RawMessage `json:"versions"`
		DistTags map[string]string          `json:"dist-tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, err
	}

	local, _ := m.ListLocal()
	installed := map[string]bool{}
	active := m.ActiveVersion()
	for _, l := range local {
		installed[l.Version] = true
		if l.Active && active == "" {
			active = l.Version
		}
	}

	var vers []string
	for v := range meta.Versions {
		vers = append(vers, v)
	}
	sort.Slice(vers, func(i, j int) bool {
		return vers[i] > vers[j]
	})

	out := make([]VersionInfo, 0, len(vers))
	for _, v := range vers {
		out = append(out, VersionInfo{
			Version:   v,
			Installed: installed[v],
			Active:    v == active,
			Tag:       classify(v),
			Path:      paths.RuntimeDir(v),
		})
	}
	return out, nil
}

// Install downloads and installs a version into ~/.dsh/runtimes/<ver> via pnpm.
func (m *Manager) Install(version string, setActive bool, progress ProgressFunc) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("版本号为空")
	}
	if err := m.resolveTools(); err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}

	dir := paths.RuntimeDir(version)
	// Incomplete prior install (e.g. koffi/CMake failure) blocks retry — wipe and redo.
	if !hasDshBinary(dir) {
		_ = os.RemoveAll(dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	pkgJSON := filepath.Join(dir, "package.json")
	safeName := strings.ReplaceAll(version, ".", "-")
	// Explicit @koromix/koffi-win32-x64: koffi marks it optional; under pnpm it is often
	// skipped, then install script fails "Failed to load prebuilt" → CMake rebuild.
	body := fmt.Sprintf(`{
  "name": "dsh-runtime-%s",
  "private": true,
  "dependencies": {
    "%s": "%s",
    "@koromix/koffi-win32-x64": "3.2.0"
  },
  "pnpm": {
    "onlyBuiltDependencies": [
      "@deepseek-ai/dsh-subprocess-local",
      "@google/genai",
      "koffi",
      "node-pty",
      "protobufjs"
    ]
  }
}
`, safeName, npmPackage, version)
	if err := os.WriteFile(pkgJSON, []byte(body), 0o644); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, ".npmrc"), []byte("engine-strict=false\nnode-linker=hoisted\nshamefully-hoist=true\n"), 0o644)

	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}

	m.mu.Lock()
	tools := m.tools
	m.mu.Unlock()

	if verOut, err := bundle.Cmd(tools, tools.Node, "-v").CombinedOutput(); err == nil {
		report(fmt.Sprintf("使用 Node %s\n", strings.TrimSpace(string(verOut))))
	}

	// Install native prebuild first so koffi's postinstall require() succeeds.
	if err := ensureKoffiNative(tools, dir, progress); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}

	report(fmt.Sprintf("pnpm add %s@%s ...\n", npmPackage, version))
	cmd := bundle.Cmd(tools, tools.Pnpm, "add", fmt.Sprintf("%s@%s", npmPackage, version), "--force")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	report(string(out))
	if err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("%w", wrapNativeInstallError(err, string(out)))
	}

	if err := verifyKoffiLoad(tools, dir, progress); err != nil {
		// one more attempt: re-add platform package after dsh tree settled
		_ = ensureKoffiNative(tools, dir, progress)
		if err2 := verifyKoffiLoad(tools, dir, progress); err2 != nil {
			_ = os.RemoveAll(dir)
			return err2
		}
	}

	report("pnpm rebuild ...\n")
	cmd2 := bundle.Cmd(tools, tools.Pnpm, "rebuild")
	cmd2.Dir = dir
	out2, err2 := cmd2.CombinedOutput()
	report(string(out2))
	if err2 != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("%w", wrapNativeInstallError(err2, string(out2)))
	}

	if !hasDshBinary(dir) {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("安装完成但未找到 dsh 可执行文件: %s", dir)
	}
	if err := markReady(dir); err != nil {
		return err
	}

	if setActive {
		return m.SetActive(version)
	}
	return nil
}

func wrapNativeInstallError(err error, out string) error {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "cmake does not seem to be available"),
		strings.Contains(low, "failed to load prebuilt binary"),
		strings.Contains(low, "koffi"):
		return fmt.Errorf("原生模块 koffi 安装失败（预编译未命中且本机无 CMake）。请安装新版 DSH Desktop（内置 Node 20），删除 %%USERPROFILE%%\\.dsh\\runtimes 后点「重新准备环境」。详情: %v", err)
	default:
		return fmt.Errorf("pnpm 安装失败: %w\n%s", err, out)
	}
}

// SetActive writes the active pointer. Caller should stop/restart process.
func (m *Manager) SetActive(version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("empty version")
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	dir := paths.RuntimeDir(version)
	legacy := paths.LegacyRun()
	ok := hasDshBinary(dir)
	if !ok && hasDshBinary(legacy) && readPackageVersion(legacy) == version {
		ok = true
	}
	if !ok {
		return fmt.Errorf("version %s is not installed", version)
	}
	return os.WriteFile(paths.ActiveFile(), []byte(version+"\n"), 0o644)
}

// DshBinary returns path to dsh.cmd / dsh / dsh.js inside a runtime dir.
func DshBinary(runtimeDir string) string {
	candidates := []string{
		filepath.Join(runtimeDir, "node_modules", ".bin", "dsh.cmd"),
		filepath.Join(runtimeDir, "node_modules", ".bin", "dsh"),
		filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "bin", "dsh.js"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func hasDshBinary(dir string) bool {
	// The .bin shims can outlive the package (dead junctions, half-copied
	// trees); only trust the pair when the real package is there too.
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "package.json")); err != nil {
		return false
	}
	return DshBinary(dir) != ""
}

func readPackageVersion(runtimeDir string) string {
	p := filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "package.json")
	b, err := os.ReadFile(p)
	if err != nil {
		b, err = os.ReadFile(filepath.Join(runtimeDir, "package.json"))
		if err != nil {
			return ""
		}
		var pkg struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		if json.Unmarshal(b, &pkg) == nil {
			return pkg.Dependencies[npmPackage]
		}
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

func classify(v string) string {
	lv := strings.ToLower(v)
	switch {
	case strings.Contains(lv, "alpha"):
		return "alpha"
	case strings.Contains(lv, "beta"):
		return "beta"
	case strings.Contains(lv, "rc"):
		return "rc"
	default:
		return "release"
	}
}

// ReadyStatus describes whether the machine can run Harness without extra setup.
type ReadyStatus struct {
	HasNode     bool   `json:"hasNode"`
	NodeOK      bool   `json:"nodeOK"`
	HasPnpm     bool   `json:"hasPnpm"`
	HasRuntime  bool   `json:"hasRuntime"`
	NodePath    string `json:"nodePath"`
	PnpmPath    string `json:"pnpmPath"`
	ActiveVer   string `json:"activeVersion"`
	NeedsSetup  bool   `json:"needsSetup"`
	Message     string `json:"message"`
}

func (m *Manager) ReadyStatus() ReadyStatus {
	st := ReadyStatus{}
	t := m.bundle.Probe()
	st.NodePath, st.PnpmPath = t.Node, t.Pnpm
	st.HasNode = t.Node != ""
	st.NodeOK = bundle.NodeSatisfiesPath(t.Node)
	st.HasPnpm = t.Pnpm != ""
	if st.HasNode && st.HasPnpm {
		m.setTools(t)
	}
	ver, _, err := m.ActiveDir()
	if err == nil {
		st.HasRuntime = true
		st.ActiveVer = ver
	}
	st.NeedsSetup = !(st.HasNode && st.HasPnpm && st.HasRuntime)
	switch {
	case st.HasNode && !st.NodeOK:
		st.Message = fmt.Sprintf("Node 过旧（需 ≥ v%s），需要升级", bundle.MinNodeSemver)
	case !st.HasNode:
		st.Message = "需要准备 Node 运行时"
	case !st.HasPnpm:
		st.Message = "需要准备 pnpm"
	case !st.HasRuntime:
		st.Message = "需要安装 DeepSeek Harness"
	default:
		st.Message = "环境已就绪"
	}
	return st
}

// Bootstrap ensures Node + pnpm + at least one dsh runtime (bundled copy or npm).
func (m *Manager) Bootstrap(progress ProgressFunc) error {
	tools, err := m.bundle.EnsureReady(func(line string) {
		if progress != nil {
			progress(line)
		}
	})
	if err != nil {
		return err
	}
	m.setTools(tools)

	if _, _, err := m.ActiveDir(); err == nil {
		if progress != nil {
			progress("已有 dsh 运行时，跳过安装\n")
		}
		return nil
	}

	if ok, err := m.TryActivateBundled(progress); err != nil {
		return err
	} else if ok {
		return nil
	}

	ver := bundle.DefaultDSH
	if ver == "" {
		ver, err = m.LatestRemoteVersion()
		if err != nil {
			return fmt.Errorf("无法解析最新 dsh 版本: %w", err)
		}
	}
	if progress != nil {
		progress(fmt.Sprintf("正在安装 @deepseek-ai/dsh@%s …\n", ver))
	}
	if err := m.Install(ver, true, progress); err != nil {
		return err
	}
	return nil
}

// LatestRemoteVersion returns npm dist-tags.latest, else the newest version key.
func (m *Manager) LatestRemoteVersion() (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://registry.npmjs.org/" + npmPackage)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("npm HTTP %d", resp.StatusCode)
	}
	var meta struct {
		DistTags map[string]string          `json:"dist-tags"`
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", err
	}
	if v := meta.DistTags["latest"]; v != "" {
		return v, nil
	}
	var vers []string
	for v := range meta.Versions {
		vers = append(vers, v)
	}
	if len(vers) == 0 {
		return "", fmt.Errorf("npm 无可用版本")
	}
	sort.Slice(vers, func(i, j int) bool { return vers[i] > vers[j] })
	return vers[0], nil
}

