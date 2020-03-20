git.exe rev-list --pretty="format:%%h %%ai" -1 HEAD > ./gitCommit
for /f "usebackq tokens=*" %%i in (gitCommit) DO set gg=%%i

cd .\cmd\uploader
go build -v -ldflags="-X 'main.gitCommit=%gg%'" 
@cd ..\..

cd .\cmd\uploadserver 
go build -v -ldflags="-X 'main.gitCommit=%gg%'" 
@cd ..\..
