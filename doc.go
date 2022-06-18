/*
Package upload is an http service that stores uploaded files.
The service can resume upload of a file if a client is 'uploader' at any time but only until this file is completed.
It has a directory in its root storage for each user.

Service consists of these packages:
./cmd/uploader 		- is a client. It can resume an upload of a file any time later if server permits.
./cmd/uploadserver 	- is a server side of an upload service. It permits resume of an upload only if a file was not uploaded completely.
./cmd/decodejournal	- is an utility used to dump the content of a journal file. Journal file is used to allow a resume of a file upload.

You build all these packages by running:
> build.ps1
*/
package upload
