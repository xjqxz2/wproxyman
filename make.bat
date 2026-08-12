@echo off
rem ============================================================================
rem  WProxyman - build script (Windows)
rem
rem  Usage:  make.bat [build|dev|test|deps|clean|doctor|tidy]
rem          (no argument = build)
rem
rem  Output: build\bin\WProxyman.exe
rem  Requires: Go 1.25+, Node.js 20+, Wails v2 CLI (auto-installed if missing)
rem ============================================================================

setlocal enabledelayedexpansion
set APP_NAME=WProxyman

if "%1"=="" goto build
if "%1"=="build" goto build
if "%1"=="dev" goto dev
if "%1"=="test" goto test
if "%1"=="smoke" goto smoke
if "%1"=="deps" goto deps
if "%1"=="clean" goto clean
if "%1"=="doctor" goto doctor
if "%1"=="tidy" goto tidy
echo Unknown target: %1
echo Usage: make.bat [build^|dev^|test^|smoke^|deps^|clean^|doctor^|tidy]
exit /b 1

rem ----------------------------------------------------------------------------
rem  Toolchain check (Go / npm / wails)
rem ----------------------------------------------------------------------------
:check-tools
where go >nul 2>&1
if errorlevel 1 (
    echo ERROR: Go not found. Install Go 1.25+ from https://go.dev/dl/
    exit /b 1
)
where npm >nul 2>&1
if errorlevel 1 (
    echo ERROR: Node.js/npm not found. Install from https://nodejs.org/
    exit /b 1
)
where wails >nul 2>&1
if errorlevel 1 (
    echo Wails CLI not found - installing...
    call go install github.com/wailsapp/wails/v2/cmd/wails@latest
    if errorlevel 1 exit /b 1
    where wails >nul 2>&1
    if errorlevel 1 (
        echo ERROR: wails not found in PATH after install.
        echo Add %%GOPATH%%\bin to your PATH, then re-run: make.bat
        exit /b 1
    )
)
exit /b 0

rem ----------------------------------------------------------------------------
rem  build
rem ----------------------------------------------------------------------------
:build
call :check-tools
if errorlevel 1 exit /b 1
echo ==^> Installing frontend dependencies...
pushd frontend
call npm install
if errorlevel 1 popd & exit /b 1
popd
echo ==^> Building %APP_NAME% (production)
call wails build
if errorlevel 1 exit /b 1
echo ==^> Running smoke test
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\smoke-test.ps1"
if errorlevel 1 exit /b 1
echo ==^> Done. Binary: build\bin\%APP_NAME%.exe
exit /b 0

rem ----------------------------------------------------------------------------
rem  dev
rem ----------------------------------------------------------------------------
:dev
call :check-tools
if errorlevel 1 exit /b 1
call wails dev
exit /b 0

rem ----------------------------------------------------------------------------
rem  test
rem ----------------------------------------------------------------------------
:test
echo ==^> Go tests
call go test ./...
if errorlevel 1 exit /b 1
echo ==^> Frontend type check
pushd frontend
call npx --no-install tsc --noEmit
set TSC_RC=%errorlevel%
popd
if not "%TSC_RC%"=="0" exit /b %TSC_RC%
exit /b 0

rem ----------------------------------------------------------------------------
rem  smoke
rem ----------------------------------------------------------------------------
:smoke
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\smoke-test.ps1"
exit /b %errorlevel%

rem ----------------------------------------------------------------------------
rem  deps
rem ----------------------------------------------------------------------------
:deps
echo ==^> Go dependencies
call go mod download
echo ==^> Frontend dependencies
pushd frontend
call npm install
popd
exit /b 0

rem ----------------------------------------------------------------------------
rem  clean
rem ----------------------------------------------------------------------------
:clean
if exist build\bin rmdir /s /q build\bin
if exist frontend\dist rmdir /s /q frontend\dist
echo Cleaned build\bin and frontend\dist.
exit /b 0

rem ----------------------------------------------------------------------------
rem  doctor
rem ----------------------------------------------------------------------------
:doctor
call :check-tools
if errorlevel 1 exit /b 1
call wails doctor
exit /b 0

rem ----------------------------------------------------------------------------
rem  tidy
rem ----------------------------------------------------------------------------
:tidy
call go mod tidy
exit /b 0
