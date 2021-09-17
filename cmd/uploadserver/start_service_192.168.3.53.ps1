$rootUpload = (git.exe rev-parse --show-toplevel)
$uploadserver = "$rootUpload/cmd/uploadserver"

pushd
Set-Location $uploadserver
.\uploadserver.exe -debug -log .\testdata\service.log -root .\testdata\storageroot\ -config .\testdata\ -listenOn 192.168.3.53:64000
popd

