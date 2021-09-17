git.exe log --pretty="format:%d %h %ae %cd" -1 > ./gitCommit
$gg = Get-Content ./gitCommit

cd .\cmd\uploader
go build -v -ldflags="-X 'main.gitCommit=$gg'" 
cd ..\..

cd .\cmd\uploadserver 
go build -v -ldflags="-X 'main.gitCommit=$gg'" 
cd ..\..

New-Item build -ItemType Directory -ErrorAction Ignore
Copy-Item .\cmd\uploader\uploader.exe .\build\
Copy-Item .\cmd\uploadserver\uploadserver.exe .\build\
Copy-Item .\cmd\uploadserver\htmltemplates .\build\ -Recurse