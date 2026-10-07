$ErrorActionPreference = 'Stop'

$repo = 'afjcjsbx/picoclaw'
try {
    $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
} catch {
    $architecture = $env:PROCESSOR_ARCHITEW6432
    if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
}
if (-not $architecture) { throw 'Unable to detect Windows architecture' }
switch ($architecture.ToUpperInvariant()) {
    'X64' { $arch = 'x86_64' }
    'AMD64' { $arch = 'x86_64' }
    'ARM64' { $arch = 'arm64' }
    default { throw "Unsupported Windows architecture: $architecture" }
}

$asset = "picoclaw_Windows_$arch.zip"
$temp = Join-Path ([IO.Path]::GetTempPath()) ("picoclaw-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'PicoClaw-Installer' }
    $tag = $release.tag_name
    $base = "https://github.com/$repo/releases/download/$tag"
    $archive = Join-Path $temp $asset
    $checksums = Join-Path $temp "picoclaw_$($tag.TrimStart('v'))_checksums.txt"
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $archive
    Invoke-WebRequest -UseBasicParsing -Uri "$base/picoclaw_$($tag.TrimStart('v'))_checksums.txt" -OutFile $checksums

    $entry = Get-Content -LiteralPath $checksums | Where-Object { $_ -match ([regex]::Escape($asset) + '$') } | Select-Object -First 1
    if (-not $entry) { throw "No checksum found for $asset" }
    $expected = ($entry -split '\s+')[0]
    $actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
    if ($actual -ne $expected) { throw "Checksum verification failed for $asset" }

    $extracted = Join-Path $temp 'extracted'
    Expand-Archive -LiteralPath $archive -DestinationPath $extracted
    $launcher = Join-Path $extracted 'picoclaw-launcher.exe'
    $picoclaw = Join-Path $extracted 'picoclaw.exe'
    if (-not (Test-Path -LiteralPath $launcher) -or -not (Test-Path -LiteralPath $picoclaw)) {
        throw 'Release archive does not contain both PicoClaw binaries'
    }

    $installDir = Join-Path $env:LOCALAPPDATA 'Programs\PicoClaw'
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Copy-Item -LiteralPath $picoclaw -Destination (Join-Path $installDir 'picoclaw.exe') -Force
    Copy-Item -LiteralPath $launcher -Destination (Join-Path $installDir 'picoclaw-launcher.exe') -Force
    Start-Process -FilePath (Join-Path $installDir 'picoclaw-launcher.exe')
    Write-Host "PicoClaw installed in $installDir; the Web UI is starting in your browser."
}
finally {
    Remove-Item -LiteralPath $temp -Recurse -Force
}
