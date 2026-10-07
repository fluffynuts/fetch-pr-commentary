#!/usr/bin/env pwsh
# fetch-pr-commentary: build, test and tidy the CLI, for machines without make.
# Mirrors the Makefile: ./make.ps1 [build|test|vet|check|clean|dist]... (default: build)
#
# GO, BINARY and PKG can be overridden from the environment, as with make.
#
# dist packages a release zip in dist/ for this machine (or for GOOS/GOARCH,
# when those are set), with the binary and whatever $Bundle (below) lists,
# under one top-level folder. BUILD and DIST_LABEL work as in make.sh. Zips
# for other platforms are better made by make.sh, whose zip(1) keeps the
# binary's executable bit.

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$Go = if ($env:GO) { $env:GO } else { 'go' }
$IsWin = $IsWindows -or $env:OS -eq 'Windows_NT'
$Binary = if ($env:BINARY) { $env:BINARY } elseif ($IsWin) { 'fetch-pr-commentary.exe' } else { 'fetch-pr-commentary' }
$Pkg = if ($env:PKG) { $env:PKG } else { './src' }
$AppCli = 'github.com/fluffynuts/fetch-pr-commentary/internal/appcli'
$Version = (Get-Content -Raw VERSION).Trim()

# What goes into the release zip beside the binary: files and folders,
# relative to the root of the repository. Most programs need nothing but a
# readme. To ship more (config, templates, docs...), add them here, e.g.
#
#   $Bundle = @('README.md', 'LICENSE', 'config', 'defaults')
#
# and add the same names to BUNDLE in make.sh.
$Bundle = @('README.md')

function Invoke-Step([string]$Command, [string[]]$Arguments) {
    Write-Host "$Command $($Arguments -join ' ')"
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

# When this build ran, for --version: Go doesn't record it itself.
function Get-BuildDate { (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'") }

# build only when a source is newer than the binary, as make would.
function Invoke-Build {
    if (Test-Path $Binary -PathType Leaf) {
        $built = (Get-Item $Binary).LastWriteTimeUtc
        $newer = Get-ChildItem -Recurse -File -Include '*.go', 'go.mod', 'go.sum', 'VERSION' |
            Where-Object { $_.FullName -notmatch '[\\/]\.git[\\/]' -and $_.LastWriteTimeUtc -gt $built } |
            Select-Object -First 1
        if (-not $newer) {
            Write-Host "'$Binary' is up to date."
            return
        }
    }
    Invoke-Step $Go @('build', '-ldflags', "-X $AppCli.Version=$Version -X $AppCli.BuildDate=$(Get-BuildDate)", '-o', $Binary, $Pkg)
}

function Invoke-Test { Invoke-Step $Go @('test', './...') }

function Invoke-Vet { Invoke-Step $Go @('vet', './...') }

function Invoke-Check {
    Invoke-Vet
    Invoke-Test
}

function Invoke-Dist {
    $goos = if ($env:GOOS) { $env:GOOS } else { (& $Go env GOOS).Trim() }
    $goarch = if ($env:GOARCH) { $env:GOARCH } else { (& $Go env GOARCH).Trim() }
    $osName = if ($goos -eq 'darwin') { 'macos' } else { $goos }
    $version = if ($env:BUILD) { "$Version.$($env:BUILD)" } else { $Version }
    $label = if ($env:DIST_LABEL) { "-$($env:DIST_LABEL)" } else { '' }
    $name = "fetch-pr-commentary-$version$label-$osName-$goarch"
    $exe = if ($goos -eq 'windows') { 'fetch-pr-commentary.exe' } else { 'fetch-pr-commentary' }
    $stage = Join-Path 'dist' $name

    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $stage, "$stage.zip"
    New-Item -ItemType Directory -Path $stage | Out-Null
    $env:CGO_ENABLED = '0'
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    Invoke-Step $Go @('build', '-trimpath', '-ldflags', "-X $AppCli.Version=$Version -X $AppCli.Build=$($env:BUILD) -X $AppCli.BuildDate=$(Get-BuildDate)", '-o', (Join-Path $stage $exe), $Pkg)
    foreach ($item in $Bundle) {
        if (Test-Path $item) {
            Copy-Item -Recurse $item $stage
        } else {
            [Console]::Error.WriteLine("make.ps1: warning: `$Bundle lists '$item', which doesn't exist")
        }
    }
    Compress-Archive -Path $stage -DestinationPath "$stage.zip"
    Remove-Item -Recurse -Force $stage
    Write-Host "$stage.zip"
}

function Invoke-Clean {
    Write-Host "rm -f $Binary"
    Remove-Item -Force -ErrorAction SilentlyContinue $Binary
    Write-Host 'rm -rf dist'
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue 'dist'
}

$targets = if ($args.Count -gt 0) { $args } else { @('build') }

foreach ($target in $targets) {
    switch ($target) {
        'build' { Invoke-Build }
        'test' { Invoke-Test }
        'vet' { Invoke-Vet }
        'check' { Invoke-Check }
        'dist' { Invoke-Dist }
        'clean' { Invoke-Clean }
        default {
            [Console]::Error.WriteLine("make.ps1: no such target '$target' (build, test, vet, check, clean, dist)")
            exit 2
        }
    }
}
