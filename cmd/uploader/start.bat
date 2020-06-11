rem .\uploader.exe --username zahar --dir .\testdata\testbackups\ --passwordfile ./login_https.json -cacert ./mkcertCA.pem -service ftp://192.168.2.2
.\uploader.exe --skipmarkAsUploaded --username zahar --dir .\testdata\testbackups\ --passwordfile ./testlogin.json -cacert ./mkcertCA.pem -service https://192.168.3.53:64002/upload
pause