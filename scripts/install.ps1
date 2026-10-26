$ErrorActionPreference = 'Stop'
$stage = 'initialization'

try {
    # Keep the bootstrap compatible with built-in Windows PowerShell 5.1 as
    # well as modern PowerShell. Older Windows PowerShell/.NET installations
    # may otherwise negotiate an obsolete TLS version with GitHub.
    $stage = 'TLS setup'
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {
        # PowerShell 7+ may not use ServicePointManager for HTTP. Safe to ignore.
    }

    $repo = 'VyxorDev/packrail'

    $stage = 'reading Packrail version'
    $versionUrl = 'https://raw.githubusercontent.com/VyxorDev/packrail/main/VERSION'
    $versionResponse = Invoke-RestMethod -UseBasicParsing $versionUrl
    $version = [string]$versionResponse
    if ([string]::IsNullOrWhiteSpace($version)) {
        throw 'Packrail VERSION is empty.'
    }
    $version = $version -replace '^\s+|\s+$', ''

    $stage = 'detecting Windows architecture'
    # RuntimeInformation.OSArchitecture is not reliable across all Windows
    # PowerShell 5.1/.NET Framework combinations. Prefer environment variables.
    $archName = [string]$env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrWhiteSpace($archName)) {
        $archName = [string]$env:PROCESSOR_ARCHITECTURE
    }
    if ([string]::IsNullOrWhiteSpace($archName)) {
        throw 'Neither PROCESSOR_ARCHITEW6432 nor PROCESSOR_ARCHITECTURE is available.'
    }
    switch -Regex ($archName) {
        '^AMD64$' { $arch = 'amd64'; break }
        '^ARM64$' { $arch = 'arm64'; break }
        default { throw "Unsupported Windows architecture: $archName" }
    }

    $asset = "prl-windows-$arch.exe"
    $tag = "v$version"
    $base = "https://github.com/$repo/releases/download/$tag"

    $stage = 'creating temporary directory'
    $systemTemp = [System.IO.Path]::GetTempPath()
    if ([string]::IsNullOrWhiteSpace([string]$systemTemp)) {
        throw 'Windows did not provide a temporary directory.'
    }
    $tempName = 'packrail-install-' + ([Guid]::NewGuid().ToString('N'))
    $temp = Join-Path $systemTemp $tempName
    New-Item -ItemType Directory -Path $temp -Force | Out-Null

    try {
        $binary = Join-Path $temp $asset
        $checksums = Join-Path $temp 'SHA256SUMS.txt'

        $stage = "downloading $asset"
        Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile $binary

        $stage = 'downloading checksum manifest'
        Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS.txt" -OutFile $checksums

        $stage = 'verifying checksum'
        $escapedAsset = [regex]::Escape($asset)
        $line = Get-Content $checksums | Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+\*?$escapedAsset$" } | Select-Object -First 1
        if (-not $line) {
            throw "Checksum for $asset was not found."
        }
        $expected = [string](($line -split '\s+')[0])
        $actualHash = Get-FileHash -Algorithm SHA256 $binary
        if ($null -eq $actualHash -or [string]::IsNullOrWhiteSpace([string]$actualHash.Hash)) {
            throw "Could not calculate SHA-256 for $asset."
        }
        if ($actualHash.Hash -ine $expected) {
            throw "SHA-256 mismatch for $asset."
        }

        $stage = 'determining install directory'
        $installDir = [string]$env:PACKRAIL_INSTALL_DIR
        if ([string]::IsNullOrWhiteSpace($installDir)) {
            $localAppData = [Environment]::GetFolderPath('LocalApplicationData')
            if ([string]::IsNullOrWhiteSpace([string]$localAppData)) {
                $localAppData = [string]$env:LOCALAPPDATA
            }
            if ([string]::IsNullOrWhiteSpace([string]$localAppData)) {
                throw 'Could not determine LocalApplicationData. Set PACKRAIL_INSTALL_DIR and try again.'
            }
            $installDir = Join-Path $localAppData 'Packrail\bin'
        }

        $stage = 'installing prl.exe'
        New-Item -ItemType Directory -Force -Path $installDir | Out-Null
        $destination = Join-Path $installDir 'prl.exe'
        Copy-Item -Force $binary $destination

        $stage = 'updating user PATH'
        $normalizedInstallDir = $installDir -replace '[\\/]+$', ''
        $userPath = [string][Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @()
        if (-not [string]::IsNullOrWhiteSpace($userPath)) {
            $entries = $userPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_) }
        }
        $alreadyInUserPath = $entries | Where-Object {
            $candidate = ([string]$_) -replace '^\s+|\s+$', ''
            $candidate = $candidate -replace '[\\/]+$', ''
            $candidate -ieq $normalizedInstallDir
        } | Select-Object -First 1
        if (-not $alreadyInUserPath) {
            if ([string]::IsNullOrWhiteSpace($userPath)) {
                $newPath = $installDir
            } else {
                $newPath = "$installDir;$userPath"
            }
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        }

        $stage = 'updating current process PATH'
        $processPath = [string]$env:Path
        $processEntries = @()
        if (-not [string]::IsNullOrWhiteSpace($processPath)) {
            $processEntries = $processPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_) }
        }
        $alreadyInProcessPath = $processEntries | Where-Object {
            $candidate = ([string]$_) -replace '^\s+|\s+$', ''
            $candidate = $candidate -replace '[\\/]+$', ''
            $candidate -ieq $normalizedInstallDir
        } | Select-Object -First 1
        if (-not $alreadyInProcessPath) {
            if ([string]::IsNullOrWhiteSpace($processPath)) {
                $env:Path = $installDir
            } else {
                $env:Path = "$installDir;$processPath"
            }
        }

        $stage = 'verifying installed executable'
        if (-not (Test-Path -LiteralPath $destination -PathType Leaf)) {
            throw "Installed executable was not found at $destination."
        }

        Write-Host "Installed Packrail $version to $destination"
        Write-Host 'Run: prl version'
    }
    finally {
        if (-not [string]::IsNullOrWhiteSpace([string]$temp) -and (Test-Path -LiteralPath $temp)) {
            Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
        }
    }
}
catch {
    $message = [string]$_.Exception.Message
    $lineNumber = [string]$_.InvocationInfo.ScriptLineNumber
    if ([string]::IsNullOrWhiteSpace($lineNumber) -or $lineNumber -eq '0') {
        throw "Packrail installer failed during '$stage': $message"
    }
    throw "Packrail installer failed during '$stage' (script line $lineNumber): $message"
}
