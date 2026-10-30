# 在VS Developer PowerShell中构建独立WinRT OCR，不安装依赖或调用识别。
param(
    [Parameter(Mandatory=$true)][string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '需要原生Windows及Visual Studio C++/Windows SDK；Mac不能验证WinRT原生编译' }
if ($env:VSCMD_ARG_TGT_ARCH -notin @('x64','arm64')) { throw '请在明确x64或arm64的VS Developer PowerShell中运行' }
if (Test-Path -LiteralPath $OutputDirectory) { throw '输出目录必须不存在，未覆盖原文件' }
$aividcutProject = Split-Path -Parent $PSScriptRoot
$aividcutSource = Join-Path $aividcutProject 'native/ocr-windows/main.cpp'
$aividcutCompiler = (Get-Command cl.exe -ErrorAction Stop).Source
$aividcutOutput = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Path $aividcutOutput | Out-Null
$aividcutExe = Join-Path $aividcutOutput 'AIVidCutOCR.exe'
$aividcutObject = Join-Path $aividcutOutput 'AIVidCutOCR.obj'
$aividcutLog = Join-Path $aividcutOutput 'build.log'
# /MT只静态链接此辅助进程的CRT，不改变MLT/Qt媒体worker的UCRT工具链。
& $aividcutCompiler /nologo /std:c++17 /EHsc /O2 /MT /utf-8 /DUNICODE /D_UNICODE $aividcutSource "/Fo$aividcutObject" "/Fe$aividcutExe" /link windowsapp.lib /SUBSYSTEM:CONSOLE /INCREMENTAL:NO 2>&1 | Out-File -LiteralPath $aividcutLog -Encoding utf8
if ($LASTEXITCODE -ne 0) { throw "OCR原生编译失败，保留日志：$aividcutLog" }
if (-not (Test-Path -LiteralPath $aividcutExe -PathType Leaf)) { throw '编译器没有产生辅助进程' }
$aividcutRecord = @{
    schemaVersion=1;component='AIVidCutOCR';engine='windows-media-ocr'
    arch=$env:VSCMD_ARG_TGT_ARCH;source='native/ocr-windows/main.cpp'
    sourceSHA256=(Get-FileHash -LiteralPath $aividcutSource -Algorithm SHA256).Hash.ToLowerInvariant()
    artifactSHA256=(Get-FileHash -LiteralPath $aividcutExe -Algorithm SHA256).Hash.ToLowerInvariant()
    compiler=$aividcutCompiler;toolset=$env:VCToolsVersion;windowsSDK=$env:WindowsSDKVersion
    packageIdentityRequired=$true;nativeCompileCompleted=$true;runtimeVerified=$false;releaseVerified=$false
}
[IO.File]::WriteAllText((Join-Path $aividcutOutput 'ocr-build-info.json'),($aividcutRecord | ConvertTo-Json -Depth 4),(New-Object Text.UTF8Encoding($false)))
Write-Output 'OCR已编译，尚未签名或运行。便携方式不能调用系统OCR；请通过MSIX包身份完成后续验收。'
