# Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
$d = "D:/uploadserver"
$listenOn = "192.168.2.4:64000"

    
Copy-Item -Path ./build/*.exe -Verbose -Destination $d
Copy-Item -Path ./build/htmltemplates  -Verbose -Destination $d -Recurse

