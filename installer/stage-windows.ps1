# The normal tag workflow and source-pinned recovery share this staging owner.
python installer/release_guard.py $env:GITHUB_REF_NAME --commit $env:GITHUB_SHA
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$ver = $env:GITHUB_REF_NAME.TrimStart("v")
foreach ($arch in @("amd64", "arm64")) {
  gh release download $env:GITHUB_REF_NAME --pattern "croptop_${ver}_windows_${arch}.zip" --dir in
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  Expand-Archive "in/croptop_${ver}_windows_${arch}.zip" "dist/windows_${arch}"
  & "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" /DVersion=$ver /DArch=$arch installer\windows.iss
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
python installer/release_guard.py $env:GITHUB_REF_NAME --commit $env:GITHUB_SHA
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
gh release upload $env:GITHUB_REF_NAME dist/croptop-setup-amd64.exe dist/croptop-setup-arm64.exe
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
