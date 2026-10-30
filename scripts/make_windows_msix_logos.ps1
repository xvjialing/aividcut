# 将明确选择的现有工程图标导出为MSIX所需尺寸，不修改原图。
param(
    [Parameter(Mandatory=$true)][string]$SourcePath,
    [Parameter(Mandatory=$true)][string]$OutputDirectory
)
$ErrorActionPreference='Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '此构建入口需要Windows System.Drawing' }
if (Test-Path -LiteralPath $OutputDirectory) { throw '图标输出目录必须不存在' }
Add-Type -AssemblyName System.Drawing
$aividcutImage = [Drawing.Image]::FromFile((Resolve-Path -LiteralPath $SourcePath).Path)
try {
    if ($aividcutImage.Width -ne $aividcutImage.Height -or $aividcutImage.Width -gt 4096) { throw '需要不超过4096像素的方形源图标' }
    New-Item -ItemType Directory -Path $OutputDirectory | Out-Null
    foreach ($aividcutSize in @(44,50,150)) {
        $aividcutBitmap = [Drawing.Bitmap]::new($aividcutSize,$aividcutSize,[Drawing.Imaging.PixelFormat]::Format32bppArgb)
        try {
            $aividcutGraphics = [Drawing.Graphics]::FromImage($aividcutBitmap)
            try {
                $aividcutGraphics.Clear([Drawing.Color]::Transparent)
                $aividcutGraphics.CompositingMode=[Drawing.Drawing2D.CompositingMode]::SourceCopy
                $aividcutGraphics.InterpolationMode=[Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                $aividcutGraphics.DrawImage($aividcutImage,0,0,$aividcutSize,$aividcutSize)
            } finally { $aividcutGraphics.Dispose() }
            $aividcutBitmap.Save((Join-Path ([IO.Path]::GetFullPath($OutputDirectory)) "Logo$aividcutSize.png"),[Drawing.Imaging.ImageFormat]::Png)
        } finally { $aividcutBitmap.Dispose() }
    }
} finally { $aividcutImage.Dispose() }
