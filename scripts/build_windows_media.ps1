# 在原生Windows上使用明确UCRT64 SDK构建worker并交给Go装配器，不自动安装软件。
param(
    [Parameter(Mandatory=$true)][string]$SdkRoot,
    [Parameter(Mandatory=$true)][string]$BuildDirectory,
    [Parameter(Mandatory=$true)][string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '需要原生Windows；Mac交叉编译不能替代此验收' }
$aividcutProject = Split-Path -Parent $PSScriptRoot
$aividcutSdk = (Resolve-Path -LiteralPath $SdkRoot).Path
if (Test-Path -LiteralPath $BuildDirectory) { throw '构建目录必须不存在，未覆盖' }
if (Test-Path -LiteralPath $OutputDirectory) { throw '装配输出必须不存在，未覆盖' }
$aividcutBuild = [IO.Path]::GetFullPath($BuildDirectory)
$aividcutOutput = [IO.Path]::GetFullPath($OutputDirectory)
$aividcutGo = (Get-Command go -ErrorAction Stop).Source
$aividcutBin = Join-Path $aividcutSdk 'bin'
$aividcutTools = @{}
foreach ($aividcutName in @('cmake.exe','ninja.exe','g++.exe','pkg-config.exe')) {
    $aividcutPath = Join-Path $aividcutBin $aividcutName
    if (-not (Test-Path -LiteralPath $aividcutPath -PathType Leaf)) { throw "SDK缺工具：$aividcutPath" }
    $aividcutTools[$aividcutName] = $aividcutPath
}
$aividcutOldPath = $env:PATH
$aividcutOldPkg = $env:PKG_CONFIG_LIBDIR
$aividcutOldPkgPath = $env:PKG_CONFIG_PATH
$aividcutOldSysroot = $env:PKG_CONFIG_SYSROOT_DIR
try {
    $env:PATH = "$aividcutBin;$env:SystemRoot\System32;$env:SystemRoot"
    $env:PKG_CONFIG_LIBDIR = Join-Path $aividcutSdk 'lib/pkgconfig'
    $env:PKG_CONFIG_PATH = $env:PKG_CONFIG_LIBDIR
    $env:PKG_CONFIG_SYSROOT_DIR = ''
    $aividcutTriple = & $aividcutTools['g++.exe'] -dumpmachine
    if ($LASTEXITCODE -ne 0 -or $aividcutTriple.Trim() -ne 'x86_64-w64-mingw32') { throw '当前入口需要明确的x64 MinGW工具链' }
    New-Item -ItemType Directory -Path $aividcutBuild | Out-Null
    $aividcutArgs = @('-S',(Join-Path $aividcutProject 'native/mlt-worker'),'-B',$aividcutBuild,
        '-G','Ninja','-DCMAKE_BUILD_TYPE=Release',"-DCMAKE_CXX_COMPILER=$($aividcutTools['g++.exe'])",
        "-DCMAKE_MAKE_PROGRAM=$($aividcutTools['ninja.exe'])", "-DCMAKE_PREFIX_PATH=$aividcutSdk",
        '-DCMAKE_FIND_USE_PACKAGE_REGISTRY=OFF','-DCMAKE_FIND_USE_SYSTEM_PACKAGE_REGISTRY=OFF',
        "-DPKG_CONFIG_EXECUTABLE=$($aividcutTools['pkg-config.exe'])", "-DCMAKE_RUNTIME_OUTPUT_DIRECTORY=$aividcutBuild/bin")
    & $aividcutTools['cmake.exe'] @aividcutArgs
    if ($LASTEXITCODE -ne 0) { throw 'Windows worker配置失败' }
    & $aividcutTools['cmake.exe'] --build $aividcutBuild --target aividcut-mlt-worker --parallel 2
    if ($LASTEXITCODE -ne 0) { throw 'Windows worker编译失败' }
    $aividcutPackageTool = Join-Path $aividcutBuild 'windows-media-package.exe'
    Push-Location $aividcutProject
    try { & $aividcutGo build -o $aividcutPackageTool ./cmd/windows-media-package } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'Go装配器构建失败' }
    $aividcutModules = @('core','avformat','qt6','xml','plus','normalize','resample') | ForEach-Object { Join-Path $aividcutSdk "lib/mlt/libmlt$_.dll" }
    $aividcutPlugins = @(
        @{source=(Join-Path $aividcutSdk 'share/qt6/plugins/platforms/qoffscreen.dll');destination='plugins/platforms/qoffscreen.dll'},
        @{source=(Join-Path $aividcutSdk 'share/qt6/plugins/platforms/qwindows.dll');destination='plugins/platforms/qwindows.dll'}
    )
    Get-ChildItem -LiteralPath (Join-Path $aividcutSdk 'share/qt6/plugins/imageformats') -Filter '*.dll' | ForEach-Object {
        $aividcutPlugins += @{source=$_.FullName;destination="plugins/imageformats/$($_.Name)"}
    }
    $aividcutConfig = @{
        id='aividcut-media-windows';version='development-test';arch='amd64';output=$aividcutOutput
        worker=(Join-Path $aividcutBuild 'bin/aividcut-mlt-worker.exe')
        ffmpeg=(Join-Path $aividcutBin 'ffmpeg.exe');ffprobe=(Join-Path $aividcutBin 'ffprobe.exe')
        modules=@($aividcutModules);plugins=@($aividcutPlugins);dllDirectories=@($aividcutBin)
        systemDlls=(Get-Content -Raw -LiteralPath (Join-Path $aividcutProject 'build/windows-system-dlls.json') | ConvertFrom-Json)
        data=(Join-Path $aividcutSdk 'share/mlt')
    }
    $aividcutConfigFile = Join-Path $aividcutBuild 'windows-runtime-config.json'
    # PowerShell 5.1的-Encoding UTF8会写BOM，Go严格JSON不接受该前缀。
    [IO.File]::WriteAllText($aividcutConfigFile, ($aividcutConfig | ConvertTo-Json -Depth 8), (New-Object Text.UTF8Encoding($false)))
    & $aividcutPackageTool -config $aividcutConfigFile -assemble
    if ($LASTEXITCODE -ne 0) { throw 'SDK缺DLL或装配失败，请查看静态plan及保留stage' }
} finally {
    $env:PATH = $aividcutOldPath
    $env:PKG_CONFIG_LIBDIR = $aividcutOldPkg
    $env:PKG_CONFIG_PATH = $aividcutOldPkgPath
    $env:PKG_CONFIG_SYSROOT_DIR = $aividcutOldSysroot
}
