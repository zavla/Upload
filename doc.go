/*
Package upload is an http service that stores uploaded files.
The service can resume upload of a file if a client is 'uploader' at any time but only until this file is completed.
It has a directory in its root storage for each user.

Service consists of these packages:
./cmd/uploader 		- is a client. It can resume upload of a file any time later until file completed.
./cmd/uploadserver 	- is a server side of an upload service.
./cmd/decodejournal	- is an utility used to dump the content of a journal file. Journal is used to allow a resume of an upload.

You build all these packages by:
> build.ps1
*/
package upload
