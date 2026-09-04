package paths

import (
	"os"
	"path/filepath"
)

// Layout:
//
//	~/.dsh/                  official sessions / plugins / cordis (untouched)
//	~/.dsh/dsh-run/          legacy single install
//	~/.dsh/runtimes/<ver>/   managed @deepseek-ai/dsh installs
//	~/.dsh/runtimes/active   active version pointer
//	~/.dsh/desktop/          shell logs
//
// Bundled toolchain (shipped next to exe, or downloaded on first run):
//
//	{exeDir}/runtime/node/node.exe
//	{exeDir}/runtime/pnpm.exe
//	%LOCALAPPDATA%/dsh-desktop/runtime/...  (fallback download location)

func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("C:\\Users\\Public", ".dsh")
	}
	return filepath.Join(h, ".dsh")
}

func LegacyRun() string   { return filepath.Join(Home(), "dsh-run") }
func RuntimesDir() string { return filepath.Join(Home(), "runtimes") }
func ActiveFile() string  { return filepath.Join(RuntimesDir(), "active") }
func RuntimeDir(v string) string {
	return filepath.Join(RuntimesDir(), sanitize(v))
}
func DesktopDir() string { return filepath.Join(Home(), "desktop") }
func ConsoleLog() string { return filepath.Join(DesktopDir(), "dsh-web-console.log") }

// AppDataRoot is %LOCALAPPDATA%\dsh-desktop — portable toolchain cache.
func AppDataRoot() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(Home(), "..", "AppData", "Local")
	}
	return filepath.Join(base, "dsh-desktop")
}

func AppRuntimeDir() string { return filepath.Join(AppDataRoot(), "runtime") }
func AppNodeDir() string    { return filepath.Join(AppRuntimeDir(), "node") }
func AppPnpmPath() string   { return filepath.Join(AppRuntimeDir(), "pnpm.exe") }

func sanitize(v string) string {
	out := make([]rune, 0, len(v))
	for _, r := range v {
		if r == '/' || r == '\\' || r == ':' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func EnsureDirs() error {
	for _, d := range []string{RuntimesDir(), DesktopDir(), AppRuntimeDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ExeDir returns the directory containing the running executable.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
