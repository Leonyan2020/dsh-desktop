; Inno Setup for DSH Desktop (Go + Wails + bundled Node/pnpm)
; Build: scripts\build_setup.ps1

#define MyAppName "DSH Desktop"
#define MyAppExeName "dsh-desktop.exe"
#define MyAppVersion "0.2.3"

[Setup]
AppId={{B8D4F0E3-6C29-4A7B-8F32-9E5D1C8A4B26}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher=leonyan2020
DefaultDirName={localappdata}\Programs\dsh-desktop
DefaultGroupName={#MyAppName}
OutputDir=dist
OutputBaseFilename=dsh-desktop_Setup
Compression=lzma
SolidCompression=yes
SetupIconFile=build\windows\icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
CloseApplications=no
RestartApplications=no
WizardStyle=modern
InfoBeforeFile=
LicenseFile=

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "在桌面创建快捷方式"; GroupDescription: "附加任务:"

[Code]
procedure ReleaseLockedFiles();
var
  ResultCode: Integer;
  AppDir: String;
begin
  Exec(ExpandConstant('{cmd}'), '/C taskkill /F /IM dsh-desktop.exe', '',
       SW_HIDE, ewWaitUntilTerminated, ResultCode);
  AppDir := ExpandConstant('{app}');
  Exec(ExpandConstant('{cmd}'),
       '/C cd /d "' + AppDir + '"' +
       ' & del dsh-desktop.exe.old 2>NUL' +
       ' & if exist dsh-desktop.exe ren dsh-desktop.exe dsh-desktop.exe.old',
       '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  ReleaseLockedFiles();
  Result := '';
end;

[Files]
Source: "{#SourcePath}\dist\dsh-desktop\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{userdesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "启动 {#MyAppName}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: files; Name: "{app}\dsh-desktop.exe.old"
