# ============================================================================
# Windows 构建后冒烟测试（make.bat build 完成后自动执行）
#
# 验证项：
#   1. 产物存在且非空；
#   2. 启动应用，等待其自动开启系统代理；
#   3. 通过代理发送 HTTP 请求（200）；
#   4. 通过代理发送 HTTPS 请求（走 MITM/隧道，200）；
#   5. 正常关闭应用，验证系统代理被清理。
#
# 任一步失败即退出非零，构建视为失败。
# ============================================================================
$ErrorActionPreference = 'Stop'

$exe = Join-Path $PSScriptRoot '..\build\bin\WProxyman.exe'
$exe = [System.IO.Path]::GetFullPath($exe)

Write-Host '==> Windows 冒烟测试开始'

# 1. 产物检查
if (-not (Test-Path $exe)) {
    Write-Error "产物不存在: $exe"
    exit 1
}
$size = (Get-Item $exe).Length
if ($size -lt 1MB) {
    Write-Error "产物异常过小 ($size bytes)"
    exit 1
}
Write-Host "   [OK] 产物存在 ($([math]::Round($size/1MB,1)) MB)"

# 2. 启动应用
$proc = Start-Process -FilePath $exe -PassThru
Write-Host "   [..] 应用已启动 (PID $($proc.Id))，等待系统代理..."

# 3. 轮询系统代理端口（应用启动后自动设置）
$port = $null
for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep -Milliseconds 500
    try {
        $k = Get-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction Stop
        if ($k.ProxyEnable -eq 1 -and $k.ProxyServer) {
            $parts = $k.ProxyServer -split ':'
            if ($parts.Count -ge 2) {
                $port = $parts[1]
                break
            }
        }
    } catch { }
}
if (-not $port) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Error '超时：未检测到系统代理端口（应用可能未启动成功）'
    exit 1
}
Write-Host "   [OK] 系统代理端口 = $port"

# 4. HTTP 代理请求
Write-Host '   [..] HTTP 请求测试...'
$httpCode = 0
try {
    $r = curl.exe -s -o NUL -w '%{http_code}' -x "http://127.0.0.1:$port" 'http://www.baidu.com/' --max-time 15
    $httpCode = [int]$r
} catch { }
if ($httpCode -ne 200) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Error "HTTP 请求失败: 状态码 $httpCode"
    exit 1
}
Write-Host "   [OK] HTTP -> $httpCode"

# 5. HTTPS 代理请求（curl/schannel 需 --ssl-no-revoke 跳过吊销检查）
Write-Host '   [..] HTTPS 请求测试...'
$httpsCode = 0
try {
    $r2 = curl.exe -s -o NUL -w '%{http_code}' --ssl-no-revoke -x "http://127.0.0.1:$port" 'https://www.baidu.com/' --max-time 15
    $httpsCode = [int]$r2
} catch { }
if ($httpsCode -ne 200) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Error "HTTPS 请求失败: 状态码 $httpsCode"
    exit 1
}
Write-Host "   [OK] HTTPS -> $httpsCode"

# 6. 正常关闭并验证系统代理清理
$proc.CloseMainWindow() | Out-Null
if (-not $proc.WaitForExit(10000)) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 800
$k2 = Get-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
if ($k2 -and $k2.ProxyEnable -eq 1) {
    Write-Error '退出后系统代理未清理'
    exit 1
}
Write-Host '   [OK] 系统代理已清理'

Write-Host '==> 冒烟测试全部通过 [OK]'
exit 0
