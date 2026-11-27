@echo off
rem Local dev launcher: bypasses ExecutionPolicy for the companion dev.ps1.
rem Usage: scripts\dev.cmd [params...]   e.g. scripts\dev.cmd -Services catalogservice,userservice
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0dev.ps1" %*
