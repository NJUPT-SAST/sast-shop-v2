#requires -Version 5.1
<#
.SYNOPSIS
    本地开发一键启动 / 停止：Caddy 网关(6660) + 后端 Go 服务，不用开多个终端。

.DESCRIPTION
    先增量编译到 bin/，再后台启动进程；日志写到 %TEMP%\sast-shop-v2-dev\logs，
    PID 记录在同目录 pids.json，用 -Stop 统一下线（按进程树 kill）。

.EXAMPLE
    .\scripts\dev.ps1
    启动网关 + 五个主服务（userservice/catalogservice/paymentservice/spotservice/errandservice）

.EXAMPLE
    .\scripts\dev.ps1 -Services catalogservice,userservice
    只起分类/店铺页需要的最小组合

.EXAMPLE
    .\scripts\dev.ps1 -Services all -NoCaddy -SkipBuild
    起全部六个服务、不动网关、跳过编译

.EXAMPLE
    .\scripts\dev.ps1 -Stop
    停掉本脚本启动的所有进程

.EXAMPLE
    .\scripts\dev.ps1 -Status
    查看网关与各服务端口/进程状态，不启动任何东西
#>
[CmdletBinding()]
param(
    [string[]]$Services = @('main'),
    [switch]$NoCaddy,
    [switch]$SkipBuild,
    [switch]$Stop,
    [switch]$Status
)

$ErrorActionPreference = 'Stop'

# cmd 包装器不会按逗号拆参数，这里统一兼容 "a,b" 与 "a","b" 两种写法。
$Services = @($Services | ForEach-Object { $_ -split ',' } |
    ForEach-Object { $_.Trim() } | Where-Object { $_ })
if ($Services.Count -eq 0) { $Services = @('main') }

$repoRoot  = Split-Path -Parent $PSScriptRoot
$stateDir  = Join-Path $env:TEMP 'sast-shop-v2-dev'
$logDir    = Join-Path $stateDir 'logs'
$pidFile   = Join-Path $stateDir 'pids.json'
$binDir    = Join-Path $repoRoot 'bin'
$caddyfile = Join-Path $repoRoot 'deploy/Caddyfile.local'
$gatewayPort = 6660

$ports = [ordered]@{
    userservice       = 1323
    catalogservice    = 1324
    paymentservice    = 1325
    spotservice       = 1326
    errandservice     = 1327
    westpocketservice = 1328
}
$mainServices = @('userservice', 'catalogservice', 'paymentservice', 'spotservice', 'errandservice')

function Test-Listening([int]$Port) {
    $null -ne (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue |
        Select-Object -First 1)
}

function Resolve-Target([string[]]$Names) {
    $result = [System.Collections.Generic.List[string]]::new()
    foreach ($name in $Names) {
        if ($name -eq 'all') {
            foreach ($key in $ports.Keys) { $result.Add($key) }
            continue
        }
        if ($name -eq 'main') {
            foreach ($key in $mainServices) { $result.Add($key) }
            continue
        }
        if (-not $ports.Contains($name)) {
            throw "未知服务：$name（可用：$($ports.Keys -join ', ')、main、all）"
        }
        $result.Add($name)
    }
    @($result | Select-Object -Unique)
}

function Stop-Dev {
    if (-not (Test-Path $pidFile)) {
        Write-Host '没有记录到本脚本启动的进程。' -ForegroundColor Yellow
        return
    }
    $state = Get-Content $pidFile -Raw | ConvertFrom-Json
    $records = @($state.processes)
    $hadCaddy = @($records | Where-Object { $_.name -eq 'caddy' }).Count -gt 0
    foreach ($record in $records) {
        $proc = Get-Process -Id $record.pid -ErrorAction SilentlyContinue
        if (-not $proc) { continue }
        Write-Host ("停止 {0} (PID {1})" -f $record.name, $record.pid)
        & taskkill.exe /PID $record.pid /T /F *> $null
    }
    Remove-Item $pidFile -Force
    Start-Sleep -Milliseconds 600
    $leftover = foreach ($name in $ports.Keys) {
        if (Test-Listening $ports[$name]) { "${name}:$($ports[$name])" }
    }
    if ($hadCaddy -and (Test-Listening $gatewayPort)) { $leftover += "caddy:$gatewayPort" }
    if ($leftover) {
        Write-Warning "以下端口仍在监听，可能不是本脚本启动的进程：$($leftover -join ', ')"
    } else {
        Write-Host '已全部停止。' -ForegroundColor Green
    }
}

function Show-Status {
    $records = @()
    if (Test-Path $pidFile) {
        $state = Get-Content $pidFile -Raw | ConvertFrom-Json
        $records = @($state.processes)
    }
    $rows = foreach ($name in $ports.Keys) {
        $port = $ports[$name]
        $record = $records | Where-Object { $_.name -eq $name } | Select-Object -First 1
        [pscustomobject]@{
            服务 = $name
            端口 = $port
            监听 = if (Test-Listening $port) { 'YES' } else { '-' }
            PID  = if ($record) { $record.pid } else { '-' }
        }
    }
    $rows += [pscustomobject]@{
        服务 = 'caddy(网关)'
        端口 = $gatewayPort
        监听 = if (Test-Listening $gatewayPort) { 'YES' } else { '-' }
        PID  = '-'
    }
    $rows | Format-Table -AutoSize | Out-String | Write-Host
    if (Test-Path $logDir) { Write-Host "日志目录：$logDir" }
}

function Start-Dev {
    New-Item -ItemType Directory -Force -Path $logDir, $binDir | Out-Null
    $targets = Resolve-Target $Services
    $started = [System.Collections.Generic.List[object]]::new()

    if (-not $NoCaddy) {
        if (Test-Listening $gatewayPort) {
            Write-Host "网关 $gatewayPort 已在监听，跳过 Caddy。" -ForegroundColor Yellow
        } else {
            $caddy = Get-Command caddy -ErrorAction SilentlyContinue
            if (-not $caddy) {
                Write-Warning "未找到 caddy，跳过网关；前端将连不上后端。安装：scoop install caddy"
            } else {
                $out = Join-Path $logDir 'caddy.out.log'
                $err = Join-Path $logDir 'caddy.err.log'
                $proc = Start-Process -FilePath $caddy.Source `
                    -ArgumentList @('run', '--config', $caddyfile) `
                    -WorkingDirectory $repoRoot `
                    -RedirectStandardOutput $out -RedirectStandardError $err `
                    -WindowStyle Hidden -PassThru
                $started.Add([pscustomobject]@{ name = 'caddy'; pid = $proc.Id; port = $gatewayPort; log = $err })
            }
        }
    }

    if (-not $SkipBuild) {
        foreach ($name in $targets) {
            Write-Host "编译 $name ..." -NoNewline
            Push-Location (Join-Path $repoRoot "internal/service/$name")
            try {
                & go build -o (Join-Path $binDir "$name.exe") ./cmd/app 2>&1 | Out-Null
                if ($LASTEXITCODE -ne 0) {
                    Pop-Location
                    throw "编译 $name 失败：在 internal/service/$name 下执行 go build 查看原因"
                }
            } finally {
                if ((Get-Location).Path -ne $repoRoot) { Pop-Location }
            }
            Write-Host ' ok' -ForegroundColor Green
        }
    }

    foreach ($name in $targets) {
        $port = $ports[$name]
        if (Test-Listening $port) {
            Write-Host "$name 的端口 $port 已被占用，跳过。" -ForegroundColor Yellow
            continue
        }
        $exe = Join-Path $binDir "$name.exe"
        if (-not (Test-Path $exe)) {
            Write-Warning "$name 尚未编译（$exe 不存在），跳过。"
            continue
        }
        $out = Join-Path $logDir "$name.out.log"
        $err = Join-Path $logDir "$name.err.log"
        $proc = Start-Process -FilePath $exe -WorkingDirectory $repoRoot `
            -RedirectStandardOutput $out -RedirectStandardError $err `
            -WindowStyle Hidden -PassThru
        $started.Add([pscustomobject]@{ name = $name; pid = $proc.Id; port = $port; log = $err })
    }

    if ($started.Count -eq 0) {
        Write-Host '没有新的进程需要启动。' -ForegroundColor Yellow
        return
    }

    # 记录 PID：保留仍在运行的旧记录，追加本次启动的。
    $existing = @()
    if (Test-Path $pidFile) {
        $state = Get-Content $pidFile -Raw | ConvertFrom-Json
        $existing = @($state.processes) |
            Where-Object { Get-Process -Id $_.pid -ErrorAction SilentlyContinue }
    }
    ConvertTo-Json -InputObject @{ processes = (@($existing) + @($started)) } -Depth 5 |
        Set-Content -Path $pidFile -Encoding utf8

    Start-Sleep -Seconds 2
    Write-Host ''
    $rows = foreach ($item in $started) {
        $alive = $null -ne (Get-Process -Id $item.pid -ErrorAction SilentlyContinue)
        $listening = Test-Listening $item.port
        [pscustomobject]@{
            服务   = $item.name
            端口   = $item.port
            状态   = if ($listening) { 'OK' } elseif ($alive) { '启动中/异常' } else { '已退出' }
            PID    = $item.pid
            日志   = $item.log
        }
    }
    $rows | Format-Table -AutoSize | Out-String | Write-Host

    $dead = @($rows | Where-Object { $_.状态 -eq '已退出' })
    if ($dead) {
        Write-Warning "有服务启动后立即退出：$($dead.服务 -join ', ')，看对应日志：$logDir"
    }
    Write-Host "日志目录：$logDir"
    Write-Host '前端另开一个终端：cd C:\program1\sast-shop-v2-frontend; pnpm dev:desktop'
    Write-Host '全部停止：.\scripts\dev.ps1 -Stop'
}

if ($Stop) { Stop-Dev } elseif ($Status) { Show-Status } else { Start-Dev }
