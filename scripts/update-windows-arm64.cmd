@echo off
setlocal

echo This updater fetches the TDrive source and builds a Windows ARM64 app in WSL.
echo Close TDrive before continuing.
echo Select the installed app under your Local AppData Programs folder.
echo Do not select the TDrive-arm64-installer.exe from Downloads.
set "TARGET_EXE="
set /p "TARGET_EXE=Enter the full path to the installed TDrive.exe: "
if not defined TARGET_EXE (
  echo No executable path was provided.
  pause
  exit /b 2
)

wsl.exe --exec bash /home/pradeep/TDrive/scripts/update-windows-arm64.sh "%TARGET_EXE%"
set "RESULT=%ERRORLEVEL%"
if not "%RESULT%"=="0" echo Update failed with exit code %RESULT%.
pause
exit /b %RESULT%
