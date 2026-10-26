$ErrorActionPreference = 'Stop'

$repo = 'VyxorDev/packrail'
$versionUrl = 'https://raw.githubusercontent.com/VyxorDev/packrail/main/VERSION'
$version = (Invoke-RestMethod -UseBasicParsing $versionUrl).Trim()
if ([string]::IsNullOrWhiteSpace($version)) { throw 'Packrail VERSION is empty.' }

$archName = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($archName) {
    'x64'   { $arch = 'amd64' }
    'arm64' { $arch = 'arm64' }
    default { throw "Unsupported Windows architecture: $archName" }
}

$asset = "prl-windows-$arch.exe"
$tag = "v$version"
$base = "https://github.com/$repo/releases/download/$tag"
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("packrail-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null

try {
    $binary = Join-Path $temp $asset
    $checksums = Join-Path $temp 'SHA256SUMS.txt'
    Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile $binary
    Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS.txt" -OutFile $checksums

    $line = Get-Content $checksums | Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+\*?$([regex]::Escape($asset))$" } | Select-Object -First 1
    if (-not $line) { throw "Checksum for $asset was not found." }
    $expected = ($line -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 $binary).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "SHA-256 mismatch for $asset." }

    $installDir = if ($env:PACKRAIL_INSTALL_DIR) { $env:PACKRAIL_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Packrail\bin' }
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    $destination = Join-Path $installDir 'prl.exe'
    Copy-Item -Force $binary $destination

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @()
    if ($userPath) { $entries = $userPath -split ';' | Where-Object { $_ } }
    if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $installDir.TrimEnd('\') })) {
        $newPath = if ($userPath) { "$installDir;$userPath" } else { $installDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    }
    if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $installDir.TrimEnd('\') })) {
        $env:Path = "$installDir;$env:Path"
    }

    Write-Host "Installed Packrail $version to $destination"
    Write-Host 'Run: prl version'
}
finally {
    Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}
