# Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
$d = "D:\uploadserver"
$listenOn = "192.168.2.4:64000"

$res = New-Item -ItemType Directory -Path $d -ErrorAction SilentlyContinue

if ($?) {
    "creating dir " + $d
    Copy-Item -Path ./uploadserver.exe -Destination $d
    Copy-Item -Path ./logins.json -Destination $d
    Copy-Item -Path ./192.168* -Destination $d
    $d + "/uploadserver.exe -log "+$d+"/uploadserver.log -listenOn "+$listenOn+" -config "+$d+" -root "+$d+"/storage" | Out-File -FilePath $d"/start.bat" -Encoding ascii
} else {
    "already exists " + $d
}
