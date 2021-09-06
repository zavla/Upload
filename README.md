# A HTTPS service to hold your backup files. #
***Key features:***
* works over HTTPS, uses certificates from user supplied PEM files.
* multi user support, holds files per user.
* uses HTTP digest authentication for checking user's passwords.
* allows continue of upload at any time, but only until the file becomes completely uploaded.
* writes to actual files through the special journal(transaction) files.
* may be run as a Windows service.
* may be run on Linux.
* server side can listen on two interfaces at a time.
* server may wait for root storage to be attached by administrator when needed after a service start.
* server responds with a list of your files at ex. GET, https://127.0.0.1:64000/upload/:yourusername/ 
* this repository includes packages for server and client sides.
* the client side (uploader) marks all successfully uploaded files with unset of 'A' attribute on Windows, or with 'user.uploaded' xattr attribute on Linux. This attribute is used in other tools from "A DBA backup files tool set.": [BackupsControl](https://github.com/zavla/BackupsControl.git), [DeleteArchivedBackups](https://github.com/zavla/DeleteArchivedBackups)
* client uploader stores user password in DPAPI if on Windows.
* client side may also upload to a ftp server.
* rotation of backup files accomplished by standalone command [DeleteArchivedBackups](https://github.com/zavla/DeleteArchivedBackups) that you run on server side from a scheduler.

#### To download a service:
~~~
go get -v github.com/zavla/upload
~~~
#### To compile binaries run at upload root directory:
~~~
or build.bat
or build.sh
~~~
This will run 'go build' for every /cmd/... with commit id compiled in.

#### Usage:
you may upload files using specialized uploader (which supports continue of upload) :
~~~
uploader.exe -username zahar -file .\testdata\testbackups\sendfile.rar  -passwordfile .\logins.json -cacert ./mkcertCA.pem -service https://127.0.0.1:64000/upload
~~~
or uploading the whole directory (no recursion) :
~~~
uploader.exe --username zahar --dir .\testdata\testbackups -passwordfile .\logins.json -cacert ./mkcertCA.pem -service https://127.0.0.1:64000/upload
~~~

#### To launch a server of the service on command line:
You will need two files in PEM format with service's certificate e.x. 127.0.0.1.pem, 127.0.0.1-key.pem. You need to generate certificate pair by yourself.  
I prefer [https://github.com/FiloSottile/mkcert](https://github.com/FiloSottile/mkcert) for this.
~~~
uploadserver.exe  -log .\testdata\service.log -root .\testdata\storageroot\ -config ./  -listenOn 127.0.0.1:64000
~~~

#### To create a Windows service on Windows run a powershell:
~~~
servicecreate.ps1
~~~

#### The Service command line parameters:
~~~
Usage: 
uploadserver -root dir [-log file] -config dir -listenOn ip:port [-listenOn2 ip:port] [-debug] [-asService]
uploadserver -adduser name -config dir

  -adduser string
    	will add a login and save a password to logins.json file in -config dir.
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