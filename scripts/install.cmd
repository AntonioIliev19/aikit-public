@echo off
setlocal
rem Offline installer; Bypass applies only to this child PowerShell process.
if "%~1"=="" (
  powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" -BinaryPath "%~dp0conmesh.exe"
) else (
  powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" -BinaryPath "%~f1"
)
if errorlevel 1 (
  echo Installation failed. Review the error above.
  if "%~1"=="" pause
  exit /b 1
)
echo Installation finished. Close and reopen your terminal application before using conmesh.
if "%~1"=="" pause
