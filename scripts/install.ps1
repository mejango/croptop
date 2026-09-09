# Installs the latest croptop release on Windows.
#   irm https://crop.top/install.ps1 | iex
$ErrorActionPreference = "Stop"
$repo = "mejango/croptop"
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
$rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" -Headers @{ "User-Agent" = "croptop" }
$ver = $rel.tag_name.TrimStart("v")
$asset = "croptop_${ver}_windows_${arch}.zip"
$dest = Join-Path $env:LOCALAPPDATA "Programs\croptop"
New-Item -ItemType Directory -Force -Path $dest | Out-Null
$tmp = Join-Path $env:TEMP $asset
Write-Host "downloading croptop $ver for windows/$arch"
Invoke-WebRequest "https://github.com/$repo/releases/download/$($rel.tag_name)/$asset" -OutFile $tmp
$sums = (Invoke-WebRequest "https://github.com/$repo/releases/download/$($rel.tag_name)/checksums.txt").Content
$hash = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLower()
if ($sums -notmatch "$hash  $asset") { throw "checksum mismatch" }
Expand-Archive -Force $tmp $dest
Remove-Item $tmp
$path = [Environment]::GetEnvironmentVariable("Path", "User")
if ($path -notlike "*$dest*") { [Environment]::SetEnvironmentVariable("Path", "$path;$dest", "User"); Write-Host "added $dest to your PATH; open a new terminal" }
Write-Host "installed $dest\croptop.exe"
Write-Host "run: croptop"
