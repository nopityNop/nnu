@echo off
echo Building NNU...

if not exist dist mkdir dist

set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o dist\nnu_windows_amd64.exe .\cmd\nnu
if %ERRORLEVEL% neq 0 goto error

set GOARCH=arm64
go build -ldflags="-s -w" -o dist\nnu_windows_arm64.exe .\cmd\nnu
if %ERRORLEVEL% neq 0 goto error

echo Build complete. Binaries in .\dist\
goto end

:error
echo Build failed!
exit /b 1

:end
