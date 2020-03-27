# Run as administartor.
# If your execution policy forbides this file execution of this file
#   Set-ExecutionPolicy -ExecutionPolicy RemoteSigned

$disk = "d:" # change HERE

$pathtobin = $disk + "/uploadserver/"
$pathtolog = $disk + "/uploadserver/"
$pathtoStorage = $disk + "/uploadserver/storage/"

New-Service -Name uploader1 -BinaryPathName $pathtobin"uploadserver.exe -log "$pathtolog"uploadserver.log -listenOn 192.168.2.4:64000 -config "$pathtobin" -root "$pathtoStorage" -asService" -DisplayName "uploader" -Description "holds backups" -StartupType Automatic