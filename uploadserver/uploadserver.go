package uploadserver

import (
	"Upload/errstr"
	"Upload/fsdriver"
	"Upload/liteimp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"

	//_ "time"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

//-----has own errors
var (
	errNoError               = *errstr.NewError("uploadserver", 0, "Success.")
	errCantStartDownload     = *errstr.NewError("uploadserver", 2, "Can not start download.")
	errFileAlreadyExists     = *errstr.NewError("uploadserver", 3, "File is already fully downloaded.")
	errCantDeletePartialFile = *errstr.NewError("uploadserver", 4, "Cant delete partial file.")
	//--
	errServerExpectsRestOfTheFile    = *errstr.NewError("uploadserver", 10, "Server expects rest of the file.")
	errConnectionReadError           = *errstr.NewError("uploadserver", 11, "Connection read error.")
	errWrongURLParameters            = *errstr.NewError("uploadserver", 12, "Query URL must set &filename parameter")
	errRequestedFileIsBusy           = *errstr.NewError("uploadserver", 13, "Requested filename is busy at the moment")
	errUnexpectedFuncReturn          = *errstr.NewError("uploadserver", 14, "Unexpected return from func.")
	errContentHeaderRequired         = *errstr.NewError("uploadserver", 15, "Content-Length header required for your new file")
	errServerFailToWriteAllbytes     = *errstr.NewError("uploadserver", 16, "Server failed to write all the bytes.")
	errClientRequestShouldBindToJson = *errstr.NewError("uploadserver", 17, "Client request should bind to json.")
	errSessionEnded                  = *errstr.NewError("uploadserver", 18, "Session has ended.")
)

type stateOfFileUpload struct {
	good       bool
	name       string
	path       string
	filestatus liteimp.JsonFileStatus
}

var clientsstates map[string]stateOfFileUpload

var usedfiles sync.Map

const constdirforfiles = "d:/temp/"

// const bindToAddress = "127.0.0.1:64000"

type writeresult struct {
	count int64
	err   error
}

// RecieveAndSendToChan reads blocks from io.ReaderCloser until io.EOF or timeout.
// Expects large Gb files from http.Request.Boby.
// Sends bytes to chReciever.
// Suppose to work within a thread that reads connection.
// Exits when error or EOF.
func RecieveAndSendToChan(c io.ReadCloser, chReciever chan []byte) error {
	const constrecieveblocklen = (1 << 16) - 1
	// timeout set in http.Server{Timeout...}
	defer close(chReciever)

	blocklen := 3 * constrecieveblocklen
	b := make([]byte, blocklen)
	i := 0
	for { // endless loop

		n, err := c.Read(b) // usually reads Request.Body
		if err != nil && err != io.EOF {
			//DEBUG!!!
			fmt.Printf("endless recieve loop : i=%d, read error = %s\n", i, err)
			return errConnectionReadError // or timeout?
		}

		if n > 0 {
			chReciever <- b[:n]
			//DEBUG!!!
			fmt.Printf("endless recieve loop : i=%d send to chReciever n = %d\n", i, n)

		}
		if err == io.EOF {
			return nil // success reading
		}

		i++
	}

}

// WriteChanneltoDisk runs in background as a goroutine. Waits for input bytes in input chan.
// Adds bytes[] to a file, using transaction log file.
// Closes files at the return.
func WriteChanneltoDisk(chSource chan []byte,
	chResult chan<- writeresult,
	dir, name string,
	destination fsdriver.PartialFileInfo) {

	// to begin open files
	namelog := fsdriver.CreatePartialFileName(name)
	wp, wa, errp, erra := fsdriver.OpenTwoCorrespondentFiles(dir, name, namelog)
	// wp = transaction log file
	// wa = actual file
	if errp != nil {
		chResult <- writeresult{0, errp}
		return
	}
	defer wp.Close()

	if erra != nil {
		chResult <- writeresult{0, erra}
		return
	}
	defer wa.Close()

	//---
	closed := false
	nbyteswritten := int64(0) // return nbyteswritten to chResult
	for !closed {
		select {
		case b, ok := <-chSource: // capacity is > 1. Waits when empty. reads when there is something.
			if !ok { // ok=false means closed

				closed = true
				chResult <- writeresult{nbyteswritten, nil} // was no write failures
			} else { // writes when chan is not empty
				if len(b) != 0 {

					fmt.Printf("WRITES BYTES len(b) = %d\n", len(b))
					successbytescount, err := fsdriver.AddBytesToFileInHunks(wa, wp, b, &destination)
					// whatIsInFile now holds last log record plus count bytes written.
					nbyteswritten += int64(successbytescount)
					if err != nil {
						// disk free space error
						// disk failure
						// files access rights wrong
						// other recoverable error and not recoverable fails
						chResult <- writeresult{nbyteswritten, err}
						// This is a server, try to alert operator and retry.
						// We have already recieved bytes, try not to loose them.
						return // ends gourouting
						//TODO: notify reciver of an write error to stop recieving bytes
					}

				}

			}
		} //select
	} //for !closed (while input chan not closed)

	//TODO: write to log file fmt.Println("WriteChanneltoDisk ended")
	return // ends gourouting
}

// StartWriteStartRecieveAndWait
func RecieveAndWriteAndWait(c_Request_Body io.ReadCloser,
	chReciever chan []byte,
	chWriteResult chan writeresult,
	constdirforfiles, name string,
	whatIsInFile fsdriver.PartialFileInfo) (error, writeresult) {

	//var whatIsInFile fsdriver.PartialFileInfo // should be validated before!

	expectedcount := whatIsInFile.Count
	// Starts goroutine in background for write operations for this connection.
	go WriteChanneltoDisk(chReciever, chWriteResult, constdirforfiles, name, whatIsInFile)
	// Reciever is in current thread, sends bytes to chReciever.
	errRecieve := RecieveAndSendToChan(c_Request_Body, chReciever) // exits when error or EOF

	// Recieve has ended by error, by timeout with error, or by EOF with nil error.
	writeresult, ok := WaitForWriteChanResult(chWriteResult, expectedcount) // waits for the end of writing
	// here ok=true means written whole file.
	if ok && expectedcount == writeresult.count {
		return nil, writeresult // SUCCESS, all bytes written
	} else {
		// server failed to write all the bytes
		// reciever faid to recieve all the bytes
		return errRecieve, writeresult
	}

}

func ConvertFileStateToJsonFileStatus(state fsdriver.FileState) *liteimp.JsonFileStatus {
	return &liteimp.JsonFileStatus{
		JsonResponse: liteimp.JsonResponse{
			Startoffset: state.Startoffset,
			Count:       state.FileSize,
		},
	}
}

// ServeAnUpload is a http request handler for upload a file.
// In URL parameter "filename" is mandatory.
// Example: curl.exe -X POST http://127.0.0.1:64000/upload?&Filename="sendfile.rar" -T .\sendfile.rar
func ServeAnUpload(c *gin.Context) {

	strSessionId, err := c.Cookie("sessionID")
	if err != nil || strSessionId == "" { // no previous session. Only new files expected.

		//generate new cookie
		sessionID := uuid.New()
		c.SetCookie("sessionId", sessionID.String(), 300, "", "", false, true)
		RequestedAnUpload(c, strSessionId) // recieves new files only
	} else {
		// load session state
		if state, found := clientsstates[strSessionId]; found {
			if state.good {
				RequestedAnUploadContinueUpload(c, state) // continue upload
				return
			}

		}
		// stale state
		delete(clientsstates, strSessionId)
		c.SetCookie("sessionID", "", 300, "", "", false, true)
		c.JSON(http.StatusConflict, gin.H{"error": errSessionEnded.SetDetails("Requested session id %s is wrong.", strSessionId)})
		return

	}

}
func RequestedAnUploadContinueUpload(c *gin.Context, expectfromclient stateOfFileUpload) {
	name := expectfromclient.name
	storagepath := expectfromclient.path
	// sync.Map to prevent uploading the same file in parallel.
	// Usedfiles is global for http server.
	_, loaded := usedfiles.LoadOrStore(name, true)
	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusConflict, gin.H{"error": errRequestedFileIsBusy.SetDetails("Requested filename is busy at the moment: %s", name)})
		return
	}
	defer usedfiles.Delete(name) // clears sync.Map after http.Handler func exits.

	// Create recieve channel to write bytes for this connection.
	// chReciever expects bytes from io.Reader to be written to file.
	chReciever := make(chan []byte, 2)
	// chWriteResult is used to send result of WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)

	// can another client update the content of the file in between our requests?
	// Get a struct with the file current size and state.
	whatIsInFile, err := fsdriver.MayUpload(expectfromclient.path, name)
	if err != nil {
		c.Error(err)

		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return

	}

	// waits from client again a json answer with fromClient.Startoffset = whatIsInFile.Startoffset+1
	var fromClient liteimp.QueryParamsToContinueUpload
	err = c.ShouldBindQuery(&fromClient)
	if err != nil {
		// client didn't request with proper params
		// We expecting URL parametres
		jsonbytes, _ := json.MarshalIndent(&fromClient, "", " ")
		msgdetails := "" + string(jsonbytes)
		c.JSON(http.StatusBadRequest, gin.H{"error": errClientRequestShouldBindToJson.SetDetails("%s", msgdetails)})
		return
	}

	if fromClient.Startoffset == whatIsInFile.Startoffset {
		// client sends propper rest of the file
		errreciver, writeresult := RecieveAndWriteAndWait(c.Request.Body,
			chReciever,
			chWriteResult,
			storagepath,
			name,
			fsdriver.PartialFileInfo{Startoffset: fromClient.Startoffset, Count: fromClient.Count})
		if errreciver != nil || writeresult.err != nil {
			// server failed to write all the bytes, OR reciver failed to recieve all the bytes
			c.JSON(http.StatusExpectationFailed, gin.H{"error": errServerFailToWriteAllbytes.SetDetails("Server has written %d bytes, expected number of bytes %d", writeresult.count, fromClient.Count)})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSeccessfullUpload})

	} else {
		jsonbytes, _ := json.MarshalIndent(whatIsInFile, "", " ")
		msgdetails := "" + string(jsonbytes)
		c.JSON(http.StatusBadRequest, gin.H{"error": errServerExpectsRestOfTheFile.SetDetails(msgdetails),
			"whatIsInFile": whatIsInFile})
	}

}

func RequestedAnUpload(c *gin.Context, strSessionId string) {

	var req liteimp.RequestForUpload
	err := c.ShouldBindQuery(&req)
	if err != nil {
		// no parameter &filename
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "Details": `curl.exe -X POST http://127.0.0.1:64000/upload?&filename="sendfile.rar" -T .\sendfile.rar`})
		return // http request ends, wrong URL.
	}
	if req.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": errWrongURLParameters.SetDetails("Expected &filename", "")})
		return
	}

	// Clean "Filename" from client: find the file and info for the file
	fullpath := filepath.Clean(req.Filename)
	name := filepath.Base(fullpath) // drop the path part

	// sync.Map to prevent uploading the same file in parallel.
	// Usedfiles is global for http server.
	_, loaded := usedfiles.LoadOrStore(name, true)
	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusConflict, gin.H{"error": errRequestedFileIsBusy.SetDetails("Requested filename is busy at the moment: %s", name)})
		return
	}
	defer usedfiles.Delete(name) // clears sync.Map after http.Handler func exits.

	storagepath := GetPathWhereToStore()
	// Get a struct with the file current size and state.
	// File may be partially uploaded.
	whatIsInFile, err := fsdriver.MayUpload(storagepath, name)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return

	}
	// here we allow upload!
	// expects from client a file length if this is a new file
	lcontentstr := c.GetHeader("Content-Length")
	lcontent := int64(0)
	if lcontentstr != "" {
		lcontent, err = strconv.ParseInt(lcontentstr, 10, 64)
		if lcontent == 0 || err != nil {
			c.JSON(http.StatusLengthRequired, gin.H{"error": errContentHeaderRequired})
			return // client should make a new request
		}
	}
	// create client state
	clientsstates = make(map[string]stateOfFileUpload)

	clientsstates[strSessionId] = stateOfFileUpload{
		good:       true,
		name:       name,
		path:       storagepath,
		filestatus: *ConvertFileStateToJsonFileStatus(whatIsInFile),
	}

	// Create recieve channel to write bytes for this connection.
	// chReciever expects bytes from io.Reader to be written to file.
	chReciever := make(chan []byte, 2)
	// chWriteResult is used to send result of WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)

	// If the file already exists we expect from client one more request.
	if whatIsInFile.Startoffset == 0 {
		// A new file to upload, no need for json in request.

		err := fsdriver.BeginNewPartialFile(storagepath, name, lcontent)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		errreciver, writeresult := RecieveAndWriteAndWait(c.Request.Body,
			chReciever,
			chWriteResult,
			storagepath,
			name,
			fsdriver.PartialFileInfo{Count: lcontent, Startoffset: 0}) // blocks current Goroutine

		if errreciver != nil || writeresult.err != nil { // reciver OR write failed
			// server failed to write all the bytes
			if errreciver != nil {
				c.Error(errreciver)
			} //log error
			if writeresult.err != nil {
				c.Error(writeresult.err)
			}
			// updqate client state
			ptrstate := clientsstates[strSessionId]
			ptrstate.filestatus.Startoffset = writeresult.count

			c.JSON(http.StatusExpectationFailed, gin.H{"error": errServerFailToWriteAllbytes.SetDetails("Server has written %d bytes, expected number of bytes %d", writeresult.count, lcontent)})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSeccessfullUpload})
		return

	} else {
		// an existing file. We send a json with filesize.

		c.JSON(http.StatusConflict, *ConvertFileStateToJsonFileStatus(whatIsInFile)) // we responded with json

		//here we already have a sessionId, we expect the client to do one more request.
		//state is on map, key is a "sessionID" in cookie

	}

}

//rename WaitForWriteToFinish
func WaitForWriteChanResult(chWriteResult chan writeresult, expectednbytes int64) (retwriteresult writeresult, ok bool) {
	// asks-waits chan for the result
	nbyteswritten := int64(0) // returns
	ok = false                // returns
	retwriteresult = writeresult{count: 0, err: errUnexpectedFuncReturn}

	// waits for chWriteResult to respond or be closed
	select {
	case retwriteresult = <-chWriteResult:
	}
	nbyteswritten = retwriteresult.count
	// here channel chWriteResult is closed
	if retwriteresult.err == nil &&
		expectednbytes == nbyteswritten {
		ok = true
	}
	// may be that only half a file was written
	// may be that only half a file was recieved
	return

}

func GetPathWhereToStore() string {
	return "./" //TODO: change per user?
}

// To send a new file:
// curl.exe -X GET http://127.0.0.1:64000/upload?"&"Filename="sendfile.rar" -T .\sendfile.rar
// func main() {

// 	router := gin.Default()
// 	router.Handle("GET", "/upload", RequestedAnUpload)

// 	//router.Run(bindToAddress)  timeouts needed
// 	s := &http.Server{
// 		Addr:              bindToAddress,
// 		Handler:           router,
// 		ReadTimeout:       30 * time.Second,
// 		WriteTimeout:      30 * time.Second,
// 		ReadHeaderTimeout: 60 * time.Second,
// 		//MaxHeaderBytes: 1000,
// 	}
// 	s.ListenAndServe()
// 	fmt.Println("Uploadserver main end.")

// }
