/*
Package upload is an http service for that stores uploaded files and a client to this service
The service can resume upload of a file if a client is uploader.
It has a directory in its root storage for each user. There may be no user if a client is just a curl or other not specialized client.

Service consists of these packages:
./cmd/uploader 		- is a client. It can resume upload of a file any time later.
./cmd/uploadserver 	- is a server side of an upload sevice.
./cmd/decodejournal	- is an utility used to dump the content of a journal file. Journal is used to allow a resume of upload.

You build all these packages by:
- make all
*/
package upload
