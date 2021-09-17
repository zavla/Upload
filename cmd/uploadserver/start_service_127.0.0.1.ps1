$rootUpload = (git.exe rev-parse --show-toplevel)
$uploadserver = "$rootUpload/cmd/uploadserver"

pushd
Set-Location $uploadserver
.\uploadserver.exe -debug -log .\testdata\service.log -root .\testdata\storageroot\ -config .\testdata\ -listenOn 127.0.0.1:64000
popd
