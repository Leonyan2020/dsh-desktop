# DSH Desktop

Windows 桌面壳：托管官方 [@deepseek-ai/dsh](https://www.npmjs.com/package/@deepseek-ai/dsh) 运行时。

**不 fork、不修改 DeepSeek Harness 源码**；官方数据仍在 `%USERPROFILE%\.dsh`。

## 能力（0.2.0）

- **主窗口直接加载官方 Harness Web UI**（不跳系统浏览器）
- **安装包内附 Node.js + pnpm**，对方电脑无需预装
- 首次启动自动准备环境并安装 npm `latest` 的 `@deepseek-ai/dsh`
- 托盘：进入 Harness / 版本管理 / 启停重启
- 多版本安装与手动切换；兼容已有 `~/.dsh/dsh-run`

## 发给别人

```powershell
.\scripts\build_setup.ps1
# 产出:
#   dist\dsh-desktop\          绿色版（exe + runtime）
#   dist\dsh-desktop_Setup.exe 安装包（需本机 Inno Setup 6）
```

对方：安装后首次启动需联网（拉 Harness 包）；之后可离线使用已装版本。

## 开发

```powershell
# 前置：Go 1.23+、Node 18+、pnpm、Wails CLI 2.12、WebView2
cd D:\Documents\dsh-desktop
wails dev
# 或
wails build
```

产出：`build\bin\dsh-desktop.exe`

## 目录约定

```
~/.dsh/                  官方会话 / 插件 / cordis（壳不搬）
~/.dsh/dsh-run/          旧式单版本安装
~/.dsh/runtimes/<ver>/   桌面端并排运行时
~/.dsh/runtimes/active   当前版本指针
~/.dsh/desktop/          壳日志
```

## 说明

预览期上游可能有破坏性更改：更新一律**手动**确认，禁止静默强升。
