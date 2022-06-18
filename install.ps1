# Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
[CmdletBinding()]
param (
    [Parameter()]
    [bool]
    $test = $false
)

$d = "D:/uploadserver"
if ($test) {
    $d = "d:/uploadserver_test"
}
$listenOn = "192.168.2.4:64000"

    
Copy-Item -Path ./build/uploadserver.exe -Verbose -Destination $d
Copy-Item -Path ./build/uploader.exe -Verbose -Destination $d"/uploader"
Copy-Item -Path ./build/htmltemplates  -Verbose -Destination $d -Recurse

