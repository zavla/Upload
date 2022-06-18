# Run as administrator.
# If your execution policy does'n allow this file execution 
#   Set-ExecutionPolicy -ExecutionPolicy RemoteSigned
# You may specify to listen on two interfaces: -listenOn , -listenOn2 

$bin = "z:/uploadserver/"
$log = "z:/uploadserver/log/"
$storage = "z:/uploadserver/storage/"

New-Service -Name uploadserver -BinaryPathName $bin"uploadserver.exe -log "$log"uploadserver.log -listenOn 192.168.2.4:64000 -config "$bin" -root "$storage" -asService" -DisplayName "uploadserver" -Description "receives databases backups" -StartupType Automatic