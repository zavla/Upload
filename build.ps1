function Update-Item {
    param (
        [Parameter(Mandatory = $true)]
        [string] $from, 

        [Parameter(Mandatory = $true)]
        [string] $to

    )
    $CurrentLocation = $from
    $PreviousLocation = $to

    $Current = Get-ChildItem -LiteralPath $CurrentLocation -Recurse -File
    $Previous = Get-ChildItem -LiteralPath $PreviousLocation -Recurse -File

    ForEach ($File in $Current) {
        If ($File.Name -in $Previous.Name) {
            If ($File.LastWriteTime -gt ($Previous | Where-Object { $_.Name -eq $File.Name }).LastWriteTime) {
                Write-Output "File has been updated: $($PreviousLocation)$($File.Name)"
                Copy-Item -Force -LiteralPath $File.FullName -Destination $PreviousLocation
            }
        }
        Else {
            Write-Output "New file detected: $($PreviousLocation)$($File.Name)"
            Copy-Item -LiteralPath $File.FullName -Destination $PreviousLocation
        }
    }

}

git.exe log --pretty="format:%h %ae %cd" -1 > ./gitCommit
$gg = Get-Content ./gitCommit

cd .\cmd\uploader
go build -v -ldflags="-X 'main.gitCommit=$gg'" 
cd ..\..

cd .\cmd\uploadserver 
go build -v -ldflags="-X 'main.gitCommit=$gg'" 
cd ..\..

New-Item build -ItemType Directory -ErrorAction Ignore
Update-Item .\cmd\uploader\uploader.exe .\build\
Update-Item .\cmd\uploadserver\uploadserver.exe .\build\
Update-Item .\cmd\uploadserver\htmltemplates .\build\ 


