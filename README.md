# A service to hold your backup files. #
***Key features:***
* works over HTTP
* may be used anonymously or with user names
* uses HTTP digest authentication for users
* allows continue of uploading at any time until the file becomes complete
* only until a file becomes complete
* may be used just by curl utility, or by specialized 'uploader' (included)
* writes to actual files through the special journal files
* the repository includes packages for server and client side
* server side may be run as a Windows service
* the client side (uploader) marks all successfully uploaded files with unset A attribute on Windows, or FS_NODUMP_FL attribute on Linux. Files attribute is used in other tools from "A DBA backup files tool set.". For ex. in [https://github.com/zavla/BackupsControl.git](https://github.com/zavla/BackupsControl.git)

##### ...a work in progress, currently is being tested. #####

#### To compile server and client side binaries run at upload root directory:
~~~
make all
~~~
This will run 'go build' for every /cmd/...

#### To test the Service with curl utility:
~~~
curl.exe -v -X POST "http://127.0.0.1:64000/upload/zahar?&filename=sendfile.rar" -T \testdata\testbackups\sendfile.rar --user zahar --digest
~~~
or you may send using specialized uploader (which supports continue of upload) :
~~~
uploader.exe --username zahar --file .\testdata\testbackups\sendfile.rar  -passwordfile .\logins.json 
~~~
or uploading the whole directory (no recursion) :
~~~
uploader.exe --username zahar --dir .\testdata\testbackups -passwordfile .\logins.json 
~~~

#### To launch the service on command line:
~~~
uploadserver.exe  -log .\testdata\service.log -root .\testdata\storageroot\ -config ./ 
~~~

#### To create a Windows service run in powershell:
~~~
New-Service -Name upload -BinaryPathName f:\Zavla_VB\GO\src\upload\cmd\uploadserver\uploadserver.exe -asService -Description "holds your backups" -StartupType Manual -log .\testdata\service.log -root .\testdata\storageroot\ -config ./ 
~~~

#### The Service has these command line parameters:
~~~
Usage of F:\Zavla_VB\GO\src\upload\cmd\uploadserver\uploadserver.exe:
  -adduser login
        will add a login and save a password to a file specified with passwordfile.
  -asService
        start as a service (windows services or linux daemon).
  -config directory
        directory with logins.json file (required).
  -listenOn address:port
        listens on specified address:port. (default "127.0.0.1:64000")
  -log file
        log file name.
  -root path
        storage root path for files (required).
~~~