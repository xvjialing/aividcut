# 使用随包SDK和命令行诊断器，原生Windows才执行；不安装软件、不调用模型。
param(
    [Parameter(Mandatory=$true)][string]$PackageRoot,
    [Parameter(Mandatory=$true)][string]$OutputDirectory,
    [string]$Project = '',
    [ValidateRange(1,100000)][int]$Frames = 300,
    [ValidateRange(0,2147483647)][int]$FromFrame = 0,
    [string]$SeekFrames = '',
    [ValidateRange(20,60000)][int]$SampleIntervalMs = 100,
    [ValidateRange(1,300)][int]$Seconds = 30,
    [ValidateRange(1,10000)][int]$Clips = 64,
    [ValidateRange(64,1920)][int]$Width = 320,
    [ValidateRange(32,1080)][int]$Height = 180,
    [switch]$VariableFrameRate
)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '需要原生Windows，本脚本不会用交叉编译代替实机诊断' }
$aividcutPackage = (Resolve-Path -LiteralPath $PackageRoot).Path
if (Test-Path -LiteralPath $OutputDirectory) { throw '输出目录必须尚不存在，不覆盖原报告或素材' }
$aividcutOutput = [IO.Path]::GetFullPath($OutputDirectory)
$aividcutParent = Split-Path -Parent $aividcutOutput
if (-not (Test-Path -LiteralPath $aividcutParent -PathType Container)) { throw '请先创建输出的父目录' }
$aividcutMedia = Join-Path $aividcutPackage 'media'
$aividcutManifestTool = Join-Path $aividcutPackage 'tools/media-runtime-manifest.exe'
$aividcutBenchmark = Join-Path $aividcutPackage 'tools/preview-benchmark.exe'
$aividcutFixture = Join-Path $aividcutPackage 'tools/preview-video-fixture.exe'
foreach ($aividcutTool in @($aividcutManifestTool,$aividcutBenchmark)) {
    if (-not (Test-Path -LiteralPath $aividcutTool -PathType Leaf)) { throw "交接包缺少诊断工具：$aividcutTool" }
}
if ($Project) {
    $aividcutProjectFile = (Resolve-Path -LiteralPath $Project).Path
} else {
    if (-not (Test-Path -LiteralPath $aividcutFixture -PathType Leaf)) { throw '交接包缺少自建素材工具' }
    if (($Width % 2) -ne 0 -or ($Height % 2) -ne 0) { throw '自建素材宽高需为偶数' }
}
New-Item -ItemType Directory -Path $aividcutOutput | Out-Null
& $aividcutManifestTool -verify -root $aividcutMedia -target-os windows -target-arch amd64 |
    Out-File -LiteralPath (Join-Path $aividcutOutput 'media-verification.json') -Encoding utf8
if ($LASTEXITCODE -ne 0) { throw '媒体清单核验失败，尚未启动媒体工具' }
$aividcutManifest = Get-Content -Raw -LiteralPath (Join-Path $aividcutMedia 'runtime.json') | ConvertFrom-Json
$aividcutEnvironment = @{
    AIVIDCUT_FFMPEG = (Join-Path $aividcutMedia $aividcutManifest.paths.ffmpeg)
    AIVIDCUT_FFPROBE = (Join-Path $aividcutMedia $aividcutManifest.paths.ffprobe)
    AIVIDCUT_MLT_WORKER = (Join-Path $aividcutMedia $aividcutManifest.paths.worker)
    AIVIDCUT_MLT_REPOSITORY = (Join-Path $aividcutMedia $aividcutManifest.paths.repository)
    AIVIDCUT_MLT_PROFILES = (Join-Path $aividcutMedia $aividcutManifest.paths.profiles)
    AIVIDCUT_MLT_DATA = (Join-Path $aividcutMedia $aividcutManifest.paths.data)
    PATH = "$(Join-Path $aividcutMedia 'bin');$env:SystemRoot\System32;$env:SystemRoot"
}
$aividcutPrevious = @{}
foreach ($aividcutKey in $aividcutEnvironment.Keys) {
    $aividcutPrevious[$aividcutKey] = [Environment]::GetEnvironmentVariable($aividcutKey,'Process')
}
try {
    foreach ($aividcutKey in $aividcutEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($aividcutKey,$aividcutEnvironment[$aividcutKey],'Process')
    }
    if (-not $Project) {
        $aividcutFixtureDirectory = Join-Path $aividcutOutput 'fixture'
        $aividcutFixtureArgs = @('-out',$aividcutFixtureDirectory,'-seconds',"$Seconds",'-clips',"$Clips",'-width',"$Width",'-height',"$Height",'-audio')
        if ($VariableFrameRate) { $aividcutFixtureArgs += '-vfr' }
        & $aividcutFixture @aividcutFixtureArgs 2>&1 | Tee-Object -FilePath (Join-Path $aividcutOutput 'fixture.log')
        if ($LASTEXITCODE -ne 0) { throw '自建素材失败，已有日志保留，未继续运行诊断' }
        $aividcutProjectFile = Join-Path $aividcutFixtureDirectory 'split.aividcut'
    }
    $aividcutReport = Join-Path $aividcutOutput 'preview.json'
    $aividcutArgs = @('-project',$aividcutProjectFile,'-out',$aividcutReport,'-from',"$FromFrame",'-frames',"$Frames",'-width',"$Width",'-height',"$Height",'-sample-interval',"${SampleIntervalMs}ms",'-timeout','10m')
    if ($SeekFrames) { $aividcutArgs += @('-seek-frames',$SeekFrames) }
    & $aividcutBenchmark @aividcutArgs 2>&1 | Tee-Object -FilePath (Join-Path $aividcutOutput 'preview.log')
    if ($LASTEXITCODE -ne 0) { throw '原生诊断未完成，请保留preview.json和日志；不能将失败结果计为性能通过' }
    Write-Host "原生拉帧与资源报告：$aividcutReport；不包含WebView、音频设备或GPU体验验收。"
} finally {
    foreach ($aividcutKey in $aividcutPrevious.Keys) {
        [Environment]::SetEnvironmentVariable($aividcutKey,$aividcutPrevious[$aividcutKey],'Process')
    }
}
