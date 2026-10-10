@echo off
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\Configure-Firewall.ps1" -Apply -Pause
