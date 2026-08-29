<#
.SYNOPSIS
    Headnet development tasks for Windows.

.DESCRIPTION
    Windows does not ship make, so this script mirrors the Makefile targets.
    The two are kept in step deliberately: a contributor on Windows must be
    able to run exactly what CI runs before pushing.

.PARAMETER Task
    The task to run. Omit it, or pass "help", to list them.

.EXAMPLE
    .\scripts\dev.ps1 dev
    .\scripts\dev.ps1 check
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Task = 'help'
)

$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$BinDir = Join-Path $RepoRoot 'bin'
$VersionPkg = 'github.com/headnet/headnet/internal/version'

# cgo is off so the binaries are static and cross-compile cleanly. The SQLite
# driver is pure Go precisely so this works.
$env:CGO_ENABLED = '0'

function Get-BuildMetadata {
    $version = '0.0.0-dev'
    $commit = 'unknown'
    try {
        $described = (git -C $RepoRoot describe --tags --always --dirty 2>$null)
        if ($LASTEXITCODE -eq 0 -and $described) { $version = $described }
        $rev = (git -C $RepoRoot rev-parse --short HEAD 2>$null)
        if ($LASTEXITCODE -eq 0 -and $rev) { $commit = $rev }
    } catch {
        # Not a git checkout, or git is unavailable. The fallbacks above are
        # honest about that rather than inventing a version.
    }
    return @{
        Version   = $version
        Commit    = $commit
        BuildDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    }
}

function Get-LdFlags {
    $meta = Get-BuildMetadata
    return "-s -w -X $VersionPkg.version=$($meta.Version) -X $VersionPkg.commit=$($meta.Commit) -X $VersionPkg.buildDate=$($meta.BuildDate)"
}

function Invoke-Step {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][scriptblock]$Body
    )
    Write-Host "==> $Name" -ForegroundColor Cyan
    & $Body
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
}

function Show-Help {
    Write-Host ''
    Write-Host 'Headnet development tasks:' -ForegroundColor White
    Write-Host ''
    $tasks = [ordered]@{
        'dev'         = 'Run the control plane against a local SQLite database'
        'dev-web'     = 'Run the web UI dev server (proxies the API to :8080)'
        'install'     = 'Install frontend dependencies'
        'build'       = 'Build every binary for the host platform'
        'build-web'   = 'Build the web UI into apps/web/dist'
        'test'        = 'Run every test suite'
        'test-go'     = 'Run the Go tests'
        'test-web'    = 'Run the frontend tests'
        'cover'       = 'Produce a Go coverage report at coverage.html'
        'lint'        = 'Run every linter'
        'fmt'         = 'Format Go and frontend sources'
        'fmt-check'   = 'Fail if anything is unformatted'
        'vuln'        = 'Scan for known vulnerabilities in Go dependencies'
        'tidy'        = 'Tidy the Go module and verify dependencies'
        'check'       = 'Everything CI runs; run this before pushing'
        'clean'       = 'Remove build output'
    }
    foreach ($entry in $tasks.GetEnumerator()) {
        Write-Host ('  {0,-12} {1}' -f $entry.Key, $entry.Value)
    }
    Write-Host ''
    Write-Host 'Usage: .\scripts\dev.ps1 <task>'
    Write-Host ''
}

function Invoke-FormatCheck {
    Write-Host '==> gofmt -l .' -ForegroundColor Cyan
    $unformatted = & gofmt -l $RepoRoot
    if ($unformatted) {
        Write-Host 'These files are not gofmt-formatted:' -ForegroundColor Red
        $unformatted | ForEach-Object { Write-Host "  $_" }
        throw 'Run ".\scripts\dev.ps1 fmt" to fix.'
    }
}

Push-Location $RepoRoot
try {
    switch ($Task.ToLowerInvariant()) {
        'help' { Show-Help }

        'dev' {
            Write-Host 'Starting the Headnet control plane on http://localhost:8080'
            Write-Host 'Health: curl http://localhost:8080/health'
            $env:HEADNET_LOG_FORMAT = 'text'
            & go run ./apps/server
        }

        'dev-web' { Invoke-Step 'pnpm dev' { pnpm --filter '@headnet/web' dev } }

        'install' { Invoke-Step 'pnpm install' { pnpm install --frozen-lockfile } }

        'build' {
            $ldflags = Get-LdFlags
            if (-not (Test-Path $BinDir)) { New-Item -ItemType Directory -Path $BinDir | Out-Null }
            Invoke-Step 'build server' {
                go build -trimpath -ldflags $ldflags -o (Join-Path $BinDir 'headnet-server.exe') ./apps/server
            }
            Invoke-Step 'build cli' {
                go build -trimpath -ldflags $ldflags -o (Join-Path $BinDir 'headnet.exe') ./apps/cli
            }
            Write-Host "Binaries written to $BinDir" -ForegroundColor Green
        }

        'build-web' { Invoke-Step 'vite build' { pnpm --filter '@headnet/web' build } }

        'test' {
            Invoke-Step 'go test' { go test -count=1 ./... }
            Invoke-Step 'vitest' { pnpm --filter '@headnet/web' test }
        }

        'test-go' { Invoke-Step 'go test' { go test -count=1 ./... } }

        'test-web' { Invoke-Step 'vitest' { pnpm --filter '@headnet/web' test } }

        'cover' {
            Invoke-Step 'go test -cover' { go test -coverprofile=coverage.out -covermode=atomic ./... }
            Invoke-Step 'go tool cover' { go tool cover -html=coverage.out -o coverage.html }
            Write-Host 'Coverage report written to coverage.html' -ForegroundColor Green
        }

        'lint' {
            Invoke-Step 'go vet' { go vet ./... }
            if (Get-Command golangci-lint -ErrorAction SilentlyContinue) {
                Invoke-Step 'golangci-lint' { golangci-lint run }
            } else {
                Write-Host 'golangci-lint is not installed; skipping (see docs/development/setup.md)' -ForegroundColor Yellow
            }
            Invoke-Step 'eslint + prettier' { pnpm --filter '@headnet/web' lint }
            Invoke-Step 'svelte-check' { pnpm --filter '@headnet/web' check }
        }

        'fmt' {
            Invoke-Step 'gofmt' { gofmt -w $RepoRoot }
            Invoke-Step 'prettier' { pnpm --filter '@headnet/web' format }
        }

        'fmt-check' { Invoke-FormatCheck }

        'vuln' {
            if (-not (Get-Command govulncheck -ErrorAction SilentlyContinue)) {
                Invoke-Step 'install govulncheck' { go install golang.org/x/vuln/cmd/govulncheck@latest }
            }
            Invoke-Step 'govulncheck' { govulncheck ./... }
        }

        'tidy' {
            Invoke-Step 'go mod tidy' { go mod tidy }
            Invoke-Step 'go mod verify' { go mod verify }
        }

        'check' {
            Invoke-FormatCheck
            Invoke-Step 'go vet' { go vet ./... }
            if (Get-Command golangci-lint -ErrorAction SilentlyContinue) {
                Invoke-Step 'golangci-lint' { golangci-lint run }
            } else {
                Write-Host 'golangci-lint is not installed; skipping (see docs/development/setup.md)' -ForegroundColor Yellow
            }
            Invoke-Step 'go test' { go test -count=1 ./... }
            Invoke-Step 'eslint + prettier' { pnpm --filter '@headnet/web' lint }
            Invoke-Step 'svelte-check' { pnpm --filter '@headnet/web' check }
            Invoke-Step 'vitest' { pnpm --filter '@headnet/web' test }
            Write-Host 'All checks passed.' -ForegroundColor Green
        }

        'clean' {
            foreach ($path in @($BinDir, (Join-Path $RepoRoot 'apps\web\dist'),
                                (Join-Path $RepoRoot 'coverage.out'),
                                (Join-Path $RepoRoot 'coverage.html'))) {
                if (Test-Path $path) { Remove-Item -Recurse -Force $path }
            }
            Write-Host 'Build output removed.' -ForegroundColor Green
        }

        default {
            Write-Host "Unknown task '$Task'." -ForegroundColor Red
            Show-Help
            exit 2
        }
    }
} finally {
    Pop-Location
}
