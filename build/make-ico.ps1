# Build build/windows/icon.ico from build/appicon.png with a full size ladder.
#
# Wails only generates the ICO when the file does NOT exist (packager.go:202), and
# deleting it breaks //go:embed build/windows/icon.ico at the bindings step, so the
# ICO has to be produced here. Frames are PNG-compressed, which is exactly what
# Wails' own winicon does (generate.go uses png.Encode), so winres.LoadICO reads it.
#
# ASCII only (parsed by PowerShell).
param(
    [string]$Source = '',
    [string]$Dest = '',
    [int[]]$Sizes = @(256, 128, 64, 48, 32, 16)
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing

if ($Source -eq '') { $Source = Join-Path (Split-Path -Parent $PSScriptRoot) 'build\appicon.png' }
if ($Dest -eq '') { $Dest = Join-Path (Split-Path -Parent $PSScriptRoot) 'build\windows\icon.ico' }

$master = [System.Drawing.Bitmap]::FromFile($Source)
Write-Output ("source: " + $Source + "  (" + $master.Width + "x" + $master.Height + ")")

$frames = @()
foreach ($s in $Sizes) {
    $bmp = New-Object System.Drawing.Bitmap -ArgumentList $s, $s, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
    $g.Clear([System.Drawing.Color]::Transparent)
    $g.DrawImage($master, 0, 0, $s, $s)
    $g.Dispose()

    $ms = New-Object System.IO.MemoryStream
    $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $frames += @{ size = $s; data = $ms.ToArray() }
    $ms.Dispose()
    $bmp.Dispose()
}
$master.Dispose()

# ICONDIR + ICONDIRENTRY[] + PNG payloads
$out = New-Object System.IO.MemoryStream
$w = New-Object System.IO.BinaryWriter($out)
$w.Write([uint16]0)                 # reserved
$w.Write([uint16]1)                 # type: icon
$w.Write([uint16]$frames.Count)

$offset = 6 + 16 * $frames.Count
foreach ($f in $frames) {
    $dim = if ($f.size -ge 256) { [byte]0 } else { [byte]$f.size }
    $w.Write($dim)                  # width  (0 means 256)
    $w.Write($dim)                  # height
    $w.Write([byte]0)               # palette count
    $w.Write([byte]0)               # reserved
    $w.Write([uint16]1)             # planes
    $w.Write([uint16]32)            # bits per pixel
    $w.Write([uint32]$f.data.Length)
    $w.Write([uint32]$offset)
    $offset += $f.data.Length
}
foreach ($f in $frames) { $w.Write($f.data) }
$w.Flush()

[System.IO.File]::WriteAllBytes($Dest, $out.ToArray())
$w.Dispose(); $out.Dispose()

Write-Output ("written: " + $Dest + "  (" + (Get-Item $Dest).Length + " bytes, " + $frames.Count + " frames)")
