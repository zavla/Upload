# A service to hold your backup files. #
***Key features:***
* works over HTTPS, certificates should be in files.
* holds files per user.
* uses HTTP digest authentication for users.
* allows continue of uploading at any time but only until the file becomes complete.
* rotation of backup files accomlished by [DeleteArchivedBackups](https://github.com/zavla/DeleteArchivedBackups)
* may be used by specialized 'uploader' (included).
* writes to actual files through the special journal(transaction) files.
* this repository includes packages for server and client side.
* server side may be run as a Windows service.
* server side may listen on two interfaces
* server side may wait for root storage to be attached
* the client side (uploader) marks all successfully uploaded files with unset of A attribute on Windows, or FS_NODUMP_FL attribute on Linux. Files Archive attribute is used in other tools from "A DBA backup files tool set.": [BackupsControl](https://github.com/zavla/BackupsControl.git), [DeleteArchivedBackups](https://github.com/zavla/DeleteArchivedBackups)

#### To compile server and client side binaries run at upload root directory:
~~~
make all
~~~
This will run 'go build' for every /cmd/...

#### To sendto  the Service with 'uploader':
you may send using specialized uploader (which supports continue of upload) :
~~~
uploader.exe -username zahar -file .\testdata\testbackups\sendfile.rar  -passwordfile .\logins.json -service https://127.0.0.1:64000/upload
~~~
or uploading the whole directory (no recursion) :
~~~
uploader.exe --username zahar --dir .\testdata\testbackups -passwordfile .\logins.json -service https://127.0.0.1:64000/upload
~~~

#### To launch the service on command line:
~~~
uploadserver.exe  -log .\testdata\service.log -root .\testdata\storageroot\ -config ./  -listenOn 127.0.0.1:64000
~~~

#### To create a Windows service run in powershell:
~~~
New-Service -Name upload -BinaryPathName f:\Zavla_VB\GO\src\upload\cmd\uploadserver\uploadserver.exe -asService -Description "holds your backups" -StartupType Manual -log .\testdata\service.log -root .\testdata\storageroot\ -config ./ -listenOn 127.0.0.1:64000
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
  -listenOn2 address:port
        listens on specified address:port. (default "127.0.0.1:64000")
  -log file
        log file name.
  -root path
        storage root path for files (required).
~~~