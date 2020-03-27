.ONESHELL:
all: git uploadserver uploader
.PHONY: all
.PHONY: uploader
.PHONY: uploadserver
.PHONY: git
git:
	git.exe rev-list --pretty="format:%%h %%ai %%ae" -1 HEAD > ./gitCommit

uploader : 
	for /f "usebackq tokens=*" %%i in (gitCommit) DO set gg=%%i
	cd .\cmd\uploader
	go build -v -ldflags="-X 'main.gitCommit=%gg%'"

uploadserver :
	for /f "usebackq tokens=*" %%i in (gitCommit) DO set gg=%%i
	cd .\cmd\uploadserver 
	go build -v -ldflags="-X 'main.gitCommit=%gg%'"
