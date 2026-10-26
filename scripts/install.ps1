$ErrorActionPreference = 'Stop'

# Keep the bootstrap compatible with the built-in Windows PowerShell 5.1 as
# well as modern PowerShell. Older Windows PowerShell/.NET installations may
# otherwise negotiate an obsolete TLS version with GitHub.
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
    # PowerShell 7+ may not use ServicePointManager for HTTP. Safe to ignore.
}

$repo = 'VyxorDev/packrail'
$versionUrl = 'https://raw.githubusercontent.com/VyxorDev/packrail/main/VERSION'
$versionResponse = Invoke-RestMethod -UseBasicParsing $versionUrl
$version = [string]$versionResponse
if ([string]::IsNullOrWhiteSpace($version)) { throw 'Packrail VERSION is empty.' }
$version = $version.Trim()

# RuntimeInformation.OSArchitecture is convenient in PowerShell 7, but it is
# not reliable across all Windows PowerShell 5.1/.NET Framework combinations.
# PROCESSOR_ARCHITEW6432 is set when a 32-bit shell runs on 64-bit Windows.
$archName = [string]$env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($archName)) {
    $archName = [string]$env:PROCESSOR_ARCHITECTURE
}
$archName = $archName.Trim().ToUpperInvariant()
switch ($archName) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
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

    $escapedAsset = [regex]::Escape($asset)
    $line = Get-Content $checksums | Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+\*?$escapedAsset$" } | Select-Object -First 1
    if (-not $line) { throw "Checksum for $asset was not found." }
    $expected = ([string](($line -split '\s+')[0])).ToLowerInvariant()
    $actual = ([string](Get-FileHash -Algorithm SHA256 $binary).Hash).ToLowerInvariant()
    if ($actual -ne $expected) { throw "SHA-256 mismatch for $asset." }

    $installDir = [string]$env:PACKRAIL_INSTALL_DIR
    if ([string]::IsNullOrWhiteSpace($installDir)) {
        $localAppData = [Environment]::GetFolderPath('LocalApplicationData')
        if ([string]::IsNullOrWhiteSpace($localAppData)) {
            throw 'Could not determine LocalApplicationData. Set PACKRAIL_INSTALL_DIR and try again.'
        }
        $installDir = Join-Path $localAppData 'Packrail\bin'
    }

    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    $destination = Join-Path $installDir 'prl.exe'
    Copy-Item -Force $binary $destination

    $normalizedInstallDir = $installDir.TrimEnd('\')
    $userPath = [string][Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @()
    if (-not [string]::IsNullOrWhiteSpace($userPath)) {
        $entries = $userPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    }
    $alreadyInUserPath = $entries | Where-Object { ([string]$_).Trim().TrimEnd('\') -ieq $normalizedInstallDir } | Select-Object -First 1
    if (-not $alreadyInUserPath) {
        $newPath = if ([string]::IsNullOrWhiteSpace($userPath)) { $installDir } else { "$installDir;$userPath" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    }

    $processPath = [string]$env:Path
    $processEntries = @()
    if (-not [string]::IsNullOrWhiteSpace($processPath)) {
        $processEntries = $processPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    }
    $alreadyInProcessPath = $processEntries | Where-Object { ([string]$_).Trim().TrimEnd('\') -ieq $normalizedInstallDir } | Select-Object -First 1
    if (-not $alreadyInProcessPath) {
        $env:Path = if ([string]::IsNullOrWhiteSpace($processPath)) { $installDir } else { "$installDir;$processPath" }
    }

    Write-Host "Installed Packrail $version to $destination"
    Write-Host 'Run: prl version'
}
finally {
    Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}
