; Inno Setup script for croptop. Built in CI: iscc /DVersion=x.y.z /DArch=amd64 installer\windows.iss
#ifndef Version
  #define Version "0.0.0"
#endif
#ifndef Arch
  #define Arch "amd64"
#endif
[Setup]
AppName=Croptop
AppVersion={#Version}
AppPublisher=mejango
AppPublisherURL=https://crop.top
DefaultDirName={autopf}\Croptop
DefaultGroupName=Croptop
OutputBaseFilename=croptop-setup-{#Arch}
OutputDir=..\dist
Compression=lzma
SolidCompression=yes
PrivilegesRequired=lowest
ChangesEnvironment=yes
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif
[Files]
Source: "..\dist\windows_{#Arch}\croptop.exe"; DestDir: "{app}"; Flags: ignoreversion
[Icons]
Name: "{group}\Croptop"; Filename: "{app}\croptop.exe"; Parameters: "serve"
Name: "{autodesktop}\Croptop"; Filename: "{app}\croptop.exe"; Parameters: "serve"; Tasks: desktopicon
[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked
Name: "startup"; Description: "Start Croptop when you sign in"; Flags: unchecked
[Registry]
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; Check: NeedsAddPath('{app}')
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "Croptop"; ValueData: """{app}\croptop.exe"" serve --no-open"; Tasks: startup; Flags: uninsdeletevalue
[Run]
Filename: "{app}\croptop.exe"; Parameters: "serve"; Description: "Open Croptop"; Flags: nowait postinstall skipifsilent
[Code]
function NeedsAddPath(Param: string): boolean;
var OrigPath: string;
begin
  if not RegQueryStringValue(HKCU, 'Environment', 'Path', OrigPath) then begin Result := True; exit; end;
  Result := Pos(';' + Param + ';', ';' + OrigPath + ';') = 0;
end;
