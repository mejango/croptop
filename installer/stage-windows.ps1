# The normal tag workflow and source-pinned recovery share this staging owner.
param(
  [string]$Tag = $env:GITHUB_REF_NAME,
  [string]$Commit = $env:GITHUB_SHA,
  [string]$SourceRoot = ".",
  [long]$ReleaseId = 0,
  [string]$Repo = "mejango/croptop"
)
$ErrorActionPreference = "Stop"
$guard = Join-Path $PSScriptRoot "release_guard.py"
function Get-StagingRelease {
  $arguments = @($guard, $Tag, "--commit", $Commit, "--repo", $Repo, "--json")
  if ($ReleaseId -gt 0) { $arguments += @("--release-id", "$ReleaseId") }
  $result = & python @arguments
  if ($LASTEXITCODE -ne 0) { throw "Release staging guard refused the operation" }
  return $result | ConvertFrom-Json
}
function Get-UniqueAsset($release, [string]$name) {
  $matchingAssets = @($release.assets | Where-Object { $_.name -eq $name })
  if ($matchingAssets.Count -ne 1 -or $matchingAssets[0].state -ne "uploaded" -or
      $matchingAssets[0].digest -notmatch '^sha256:[0-9a-f]{64}$') {
    throw "Missing, duplicate, incomplete, or unhashed asset: $name"
  }
  return $matchingAssets[0]
}
function Assert-FileDigest([string]$path, [string]$digest) {
  if ("sha256:$((Get-FileHash $path -Algorithm SHA256).Hash.ToLowerInvariant())" -ne $digest) {
    throw "Checksum mismatch: $path"
  }
}
Push-Location $SourceRoot
try {
  $actualCommit = & git rev-parse HEAD
  if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $Commit) { throw "Installer source is not the pinned commit" }
  $dirtyInstaller = & git status --porcelain --untracked-files=all -- installer/windows.iss installer/Croptop.ico
  if ($LASTEXITCODE -ne 0 -or $dirtyInstaller) { throw "Pinned installer inputs were modified" }
  $release = Get-StagingRelease
  $ReleaseId = $release.id
  $manifestAsset = Get-UniqueAsset $release "checksums.txt"
  gh release download $Tag --repo $Repo --pattern checksums.txt --dir in
  if ($LASTEXITCODE -ne 0) { throw "Cannot download the staged checksum manifest" }
  Assert-FileDigest "in/checksums.txt" $manifestAsset.digest
  $checksums = @{}
  foreach ($line in Get-Content "in/checksums.txt") {
    $parsed = $line -match '^([0-9a-f]{64})\s+([^\s]+)$'
    if (-not $parsed -or $checksums.ContainsKey($Matches[2])) {
      throw "Invalid or duplicate checksum manifest entry"
    }
    $checksums[$Matches[2]] = "sha256:$($Matches[1])"
  }
  $originalAssets = @($manifestAsset)
  foreach ($name in $checksums.Keys) {
    $asset = Get-UniqueAsset $release $name
    if ($asset.digest -ne $checksums[$name]) { throw "Manifest and GitHub asset digest differ: $name" }
    $originalAssets += $asset
  }
  $ver = $Tag.TrimStart("v")
  foreach ($arch in @("amd64", "arm64")) {
    $archive = "croptop_${ver}_windows_${arch}.zip"
    if (-not $checksums.ContainsKey($archive)) { throw "Windows archive is not checksummed: $archive" }
    gh release download $Tag --repo $Repo --pattern $archive --dir in
    if ($LASTEXITCODE -ne 0) { throw "Cannot download the staged Windows archive" }
    Assert-FileDigest "in/$archive" $checksums[$archive]
    Expand-Archive "in/$archive" "dist/windows_${arch}"
    & "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" /DVersion=$ver /DArch=$arch installer\windows.iss
    if ($LASTEXITCODE -ne 0) { throw "Windows installer compilation failed" }
  }
  foreach ($arch in @("amd64", "arm64")) {
    $name = "croptop-setup-${arch}.exe"
    $release = Get-StagingRelease
    $existing = @($release.assets | Where-Object { $_.name -eq $name })
    if ($existing.Count -gt 0) {
      $asset = Get-UniqueAsset $release $name
      Assert-FileDigest "dist/$name" $asset.digest
      Write-Output "Identical installer already staged: $name"
      continue
    }
    # Never overwrite assets; a concurrent writer causes upload to fail closed.
    gh release upload $Tag "dist/$name" --repo $Repo
    if ($LASTEXITCODE -ne 0) { throw "Windows installer upload failed" }
  }
  $release = Get-StagingRelease
  foreach ($original in $originalAssets) {
    $current = Get-UniqueAsset $release $original.name
    if ($current.id -ne $original.id -or $current.size -ne $original.size -or $current.digest -ne $original.digest) {
      throw "An original staging asset changed: $($original.name)"
    }
  }
  foreach ($arch in @("amd64", "arm64")) {
    $name = "croptop-setup-${arch}.exe"
    $asset = Get-UniqueAsset $release $name
    Assert-FileDigest "dist/$name" $asset.digest
  }
} finally {
  Pop-Location
}
