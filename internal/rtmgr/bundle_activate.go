package rtmgr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dsh-desktop/internal/bundle"
	"dsh-desktop/internal/paths"
)

// BundledDshRoot is {exeDir}/bundled-dsh shipped inside the installer.
func BundledDshRoot() string {
	return filepath.Join(paths.ExeDir(), "bundled-dsh")
}

// readyMarker marks a managed runtime as completely expanded. Without it a
// half-copied tree (bin shims land first, packages minutes later) would look
// installed and get launched prematurely.
const readyMarker = ".dsh-desktop-ready"

// RuntimeReady reports whether dir holds a fully expanded managed runtime.
func RuntimeReady(dir string) bool {
	if !hasDshBinary(dir) {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, readyMarker))
	return err == nil
}

func markReady(dir string) error {
	return os.WriteFile(filepath.Join(dir, readyMarker), []byte("ok\n"), 0o644)
}

// TryActivateBundled copies a prebuilt runtime from the installer into ~/.dsh/runtimes.
// Returns true if a bundled runtime was activated.
func (m *Manager) TryActivateBundled(progress ProgressFunc) (bool, error) {
	root := BundledDshRoot()
	meta := filepath.Join(root, "version.txt")
	b, err := os.ReadFile(meta)
	if err != nil {
		return false, nil
	}
	ver := strings.TrimSpace(string(b))
	if ver == "" {
		return false, nil
	}
	src := filepath.Join(root, ver)
	if !hasDshBinary(src) {
		// also allow flat layout bundled-dsh/ with package directly
		if hasDshBinary(root) {
			src = root
		} else {
			return false, nil
		}
	}
	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	dest := paths.RuntimeDir(ver)
	if RuntimeReady(dest) {
		report(fmt.Sprintf("本地已有运行时 %s，跳过解包\n", ver))
		_ = m.SetActive(ver)
		return true, nil
	}
	report(fmt.Sprintf("正在展开安装包内置 Harness %s（数百 MB，请耐心等待）…\n", ver))
	_ = os.RemoveAll(dest)
	if err := copyDir(src, dest); err != nil {
		_ = os.RemoveAll(dest)
		return false, err
	}
	if !hasDshBinary(dest) {
		_ = os.RemoveAll(dest)
		return false, fmt.Errorf("内置运行时无效: %s", src)
	}
	if err := markReady(dest); err != nil {
		_ = os.RemoveAll(dest)
		return false, err
	}
	if err := m.SetActive(ver); err != nil {
		return false, err
	}
	report(fmt.Sprintf("已激活内置 Harness %s\n", ver))
	return true, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// EnsureKoffiNative forces the Windows prebuild package so koffi's install script
// can require() without falling back to CMake.
func ensureKoffiNative(tools bundle.Tools, dir string, progress ProgressFunc) error {
	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	report("确保 @koromix/koffi-win32-x64 预编译包…\n")
	cmd := bundle.Cmd(tools, tools.Pnpm, "add", "@koromix/koffi-win32-x64@3.2.0", "--force")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	report(string(out))
	if err != nil {
		return fmt.Errorf("安装 koffi-win32-x64 失败: %w\n%s", err, string(out))
	}
	return nil
}

func verifyKoffiLoad(tools bundle.Tools, dir string, progress ProgressFunc) error {
	js := "try{require('koffi');console.log('koffi-ok')}catch(e){console.error(String(e));process.exit(1)}"
	cmd := bundle.Cmd(tools, tools.Node, "-e", js)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if progress != nil {
		progress(string(out))
	}
	if err != nil {
		return fmt.Errorf("koffi 无法加载（缺少预编译原生包）: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
