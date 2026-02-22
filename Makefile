build:
	@cmd /C scripts\build.bat

dev: build
	@cmd /C dist\nnu_windows_amd64.exe
