package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseWebURLLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "官方打印行",
			line: "dsh web: http://127.0.0.1:3080/?token=j3uhDqC5LywqKwLZLlTN_FQduptgMv_bCYHe3LDPqBU",
			want: "http://127.0.0.1:3080/?token=j3uhDqC5LywqKwLZLlTN_FQduptgMv_bCYHe3LDPqBU",
		},
		{
			name: "带 LAN 后缀",
			line: "dsh web: http://127.0.0.1:3080/?token=abc (LAN: http://192.168.1.5:3080/?token=abc)",
			want: "http://127.0.0.1:3080/?token=abc",
		},
		{
			name: "CRLF 结尾",
			line: "dsh web: http://127.0.0.1:3080/?token=abc\r",
			want: "http://127.0.0.1:3080/?token=abc",
		},
		{name: "普通日志行", line: "[dsh-plugin-learn-lab] workspace id: 0c133d37", want: ""},
		{name: "裸 URL 无 token 也照收", line: "dsh web: http://127.0.0.1:3080/", want: "http://127.0.0.1:3080/"},
		{name: "非 http 前缀", line: "dsh web: tcp://127.0.0.1:3080", want: ""},
		{name: "URL 跨行不算", line: "dsh web:", want: ""},
	}
	for _, c := range cases {
		if got := parseWebURLLine(c.line); got != c.want {
			t.Errorf("%s: parseWebURLLine(%q) = %q, want %q", c.name, c.line, got, c.want)
		}
	}
}

// 分片写入：URL 行可能被管道拆成多次 Write，必须能拼回完整行。
func TestTeeWriterCapturesURLAcrossChunks(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "console.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	m := New()
	tw := &teeWriter{file: f, mgr: m}
	parts := []string{
		"[plugin] loading\n系",
		"统找不到指定的路径。\ndsh web: http://127.0.0.",
		"1:3080/?token=tok123\nmore output without newline tail",
	}
	for _, p := range parts {
		if _, err := tw.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	got := m.webURL
	m.mu.Unlock()
	want := "http://127.0.0.1:3080/?token=tok123"
	if got != want {
		t.Fatalf("webURL = %q, want %q", got, want)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "dsh web: http://127.0.0.1:3080/?token=tok123") {
		t.Fatalf("console log missing URL line, got %q", string(logged))
	}
}

// isolateConsoleLog 让不关心日志兜底的测试不受本机真实 dsh 日志影响。
func isolateConsoleLog(t *testing.T) {
	orig := consoleLogPath
	consoleLogPath = filepath.Join(t.TempDir(), "no-such-console.log")
	t.Cleanup(func() { consoleLogPath = orig })
}

func TestStatusPrefersTokenURL(t *testing.T) {
	isolateConsoleLog(t)
	m := New()
	if got := m.Status().URL; got != "http://127.0.0.1:3080" {
		t.Fatalf("bare fallback URL = %q", got)
	}
	m.mu.Lock()
	m.webURL = "http://127.0.0.1:3080/?token=abc"
	m.mu.Unlock()
	if got := m.Status().URL; got != "http://127.0.0.1:3080/?token=abc" {
		t.Fatalf("status URL = %q, want token URL", got)
	}
}

func TestWaitWebURL(t *testing.T) {
	isolateConsoleLog(t)
	m := New()
	if m.WaitWebURL(50 * 1e6) { // 50ms
		t.Fatal("WaitWebURL should time out on empty URL")
	}
	go func() {
		m.mu.Lock()
		m.webURL = "http://127.0.0.1:3080/?token=late"
		m.mu.Unlock()
	}()
	if !m.WaitWebURL(3 * 1e9) {
		t.Fatal("WaitWebURL should return once URL is set")
	}
}

// stdout 漏采时，从控制台日志尾部兜底找回认证 URL。
func TestHealWebURLFromLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "console.log")
	body := "[plugin] a\n\xbc\xed\xd3\xd2\xb2\xbb\xd4\xda\n" + // GBK 乱码行
		"dsh web: http://127.0.0.1:3080/?token=old\n系统找不到指定的路径。\n" +
		"dsh web: http://127.0.0.1:3080/?token=new\nmore\n"
	if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := consoleLogPath
	consoleLogPath = logPath
	defer func() { consoleLogPath = orig }()

	m := New()
	m.mu.Lock()
	m.healWebURLFromLog()
	got := m.webURL
	m.mu.Unlock()
	if got != "http://127.0.0.1:3080/?token=new" {
		t.Fatalf("healed webURL = %q, want newest token URL", got)
	}

	// 已有 URL 时不再扫描覆盖
	m.mu.Lock()
	m.webURL = "http://127.0.0.1:3080/?token=kept"
	m.healWebURLFromLog()
	kept := m.webURL
	m.mu.Unlock()
	if kept != "http://127.0.0.1:3080/?token=kept" {
		t.Fatalf("heal overwrote existing URL: %q", kept)
	}

	// 2 秒内不重复扫描（节流）
	m3 := New()
	m3.mu.Lock()
	m3.logScanAt = time.Now()
	m3.healWebURLFromLog()
	if m3.webURL != "" {
		t.Fatal("throttled heal should not set webURL")
	}
	m3.mu.Unlock()
}
