# A HTTPS service to hold your backup files. #
***Key features:***
* works over HTTPS, uses certificates from files.
* multi user support, holds files per user.
* uses HTTP digest authentication for users.
* allows upload continue at any time, but only until the file becomes complete.
* writes to actual files through the special journal(transaction) files.
* may be run as a Windows service.
* server side may listen on two interfaces at a time.
* server may wait for root storage be attached by administrator when needed.
* this repository includes packages for server and client side.
* the client side (uploader) marks all successfully uploaded files with unset of A attribute on Windows, or FS_NODUMP_FL attribute on Linux. Files Archive attribute is used in other tools from "A DBA backup files tool set.": [BackupsControl](https://github.com/zavla/BackupsControl.git), [DeleteArchivedBackups](https://github.com/zavla/DeleteArchivedBackups)
* rotation of backup files accomlished by standalone command [DeleteArchivedBackups](https://github.com/zavla/DeleteArchivedBackups).
* client side may also upload to ftp.

#### To download a service:
~~~
go get -v github.com/zavla/upload
~~~
#### To compile binaries run at upload root directory:
~~~
make all
or build.bat
or go build ./cmd/...
~~~
This will run 'go build' for every /cmd/... with commit id compiled in.

#### Usage:
you may send using specialized uploader (which supports continue of upload) :
~~~
uploader.exe -username zahar -file .\testdata\testbackups\sendfile.rar  -passwordfile .\logins.json -cacert ./mkcertCA.pem -service https://127.0.0.1:64000/upload
~~~
or uploading the whole directory (no recursion) :
~~~
uploader.exe --username zahar --dir .\testdata\testbackups -passwordfile .\logins.json -cacert ./mkcertCA.pem -service https://127.0.0.1:64000/upload
~~~

#### To launch a server of the service on command line:
You will need two file in PEM format with services certificates e.x. 127.0.0.1.pem, 127.0.0.1-key.pem
~~~
uploadserver.exe  -log .\testdata\service.log -root .\testdata\storageroot\ -config ./  -listenOn 127.0.0.1:64000
~~~

#### To create a Windows service run in powershell:
~~~
servicecreate.ps1
~~~

#### The Service command line parameters:
~~~
Usage: 
uploadserver -root dir [-log file] -config dir -listenOn ip:port [-listenOn2 ip:port] [-debug] [-asService]
uploadserver -adduser name -config dir

  -adduser string
    	will add a login and save a password to logins.josn file in -config dir.
  -asService
    	start as a Windows service.
  -config directory
    	directory with logins.json file (required).
  -debug
    	debug, make available /debug/pprof/* URLs in service for profile
  -listenOn address:port
    	listen on specified address:port. (default "127.0.0.1:64000")
  -listenOn2 address:port
    	listen on specified address:port.
  -log file
    	log file name.
  -root path
    	storage root path for files.
  -version version
    	print version
~~~