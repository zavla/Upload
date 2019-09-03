package uploadserver

import (
	"Upload/errstr"
	"Upload/fsdriver"
	"Upload/liteimp"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

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
	errWrongFuncParameters           = *errstr.NewError("uploadserver", 19, "Wrong func parameters.")
	errSha1CheckFailed               = *errstr.NewError("uploadserver", 20, "Sha1 check failed.")
)

type stateOfFileUpload struct {
	good       bool
	name       string
	path       string
	filestatus liteimp.JsonFileStatus
}

var clientsstates map[string]stateOfFileUpload

var usedfiles sync.Map
var emptysha1 [20]byte

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
	i := 0
	for { // endless loop

		b := make([]byte, blocklen) // must allocate for every read, else reads will overwrite previous b
		n, err := c.Read(b)         // usually reads Request.Body
		if err != nil && err != io.EOF {
			//DEBUG!!!
			log.Printf("endless recieve loop : i=%d, read error = %s\n", i, err)
			return errConnectionReadError // or timeout?
		}

		if n > 0 {
			chReciever <- b[:n]
			//DEBUG!!!
			log.Printf("endless recieve loop : i=%d send to chReciever n = %d\n", i, n)

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
	ver, wp, wa, errp, erra := fsdriver.OpenTwoCorrespondentFiles(dir, name, namelog)
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
		case b, ok := <-chSource: // capacity is > 1. Waits only when chSource is empty. Reads when there is something.
			if !ok { // ok=false means closed

				closed = true
				chResult <- writeresult{nbyteswritten, nil} // was no write failures
			} else { // writes when chan is not empty
				if len(b) != 0 {

					log.Printf("WRITES BYTES len(b) = %d to destination offset %d\n", len(b), destination.Startoffset)
					successbytescount, err := fsdriver.AddBytesToFileInHunks(wa, wp, b, ver, &destination)
					log.Printf("after AddBytesToFileInHunks destination offset %d\n", destination.Startoffset)

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

	log.Println("WriteChanneltoDisk ended.")
	return // ends gourouting
}

// StartWriteStartRecieveAndWait
func RecieveAndWriteAndWait(c_Request_Body io.ReadCloser,
	chReciever chan []byte,
	chWriteResult chan writeresult,
	constdirforfiles, name string,
	whatwhere fsdriver.PartialFileInfo) (error, writeresult) {

	expectedcount := whatwhere.Count
	if expectedcount < 0 { // should never happen, why we expect more than filesize
		// check input params
		currerr := errWrongFuncParameters.SetDetails("parameter 'whatIsInFile.Count' == %d", expectedcount)
		return currerr, writeresult{
			count: 0,
			err:   currerr,
		}
	}

	// Starts goroutine in background for write operations for this connection.
	go WriteChanneltoDisk(chReciever, chWriteResult, constdirforfiles, name, whatwhere)

	// Reciever works in current thread, sends bytes to chReciever.
	// Reciever may end with error, by timeout with error, or by EOF with nil error.
	errRecieve := RecieveAndSendToChan(c_Request_Body, chReciever) // exits when error or EOF

	// here reciever has ended. We wait for write G to complete.
	writeresult, ok := WaitForWriteChanResult(chWriteResult, expectedcount) // waits for the end of writing

	// here ok=true means we have written the whole file.
	if ok && expectedcount == writeresult.count {
		return nil, writeresult // SUCCESS, all bytes written
	}
	// server failed to write all the bytes
	// or reciever failed to recieve all the bytes
	return errRecieve, writeresult

}

func ConvertFileStateToJsonFileStatus(state fsdriver.FileState) *liteimp.JsonFileStatus {
	return &liteimp.JsonFileStatus{
		JsonResponse: liteimp.JsonResponse{
			Startoffset: state.Startoffset,
			Count:       state.FileSize,
		},
	}
}

type Logline struct {
	gin.LogFormatterParams
}

// logParams mimics defaultLogger format log line
func LogMsg(c *gin.Context, msg string) (ret Logline) {
	ret = Logline{gin.LogFormatterParams{
		Request: c.Request,
		IsTerm:  false,
	},
	}
	ret.TimeStamp = time.Now()
	ret.ClientIP = c.ClientIP()
	ret.Path = c.Request.URL.Path + "?" + c.Request.URL.RawQuery
	strSessionId := c.GetString(liteimp.KeysessionID)
	ret.ErrorMessage = fmt.Sprintf("In session id %s: '%s'", strSessionId, msg)
	return // named
}

// String used to print msg to a log.out
func (param Logline) String() string {

	var statusColor, methodColor, resetColor string

	return fmt.Sprintf(" |%s %3d %s| %13v | %15s |%s %-7s %s %s\n%s",
		//param.TimeStamp.Format(time.RFC3339Nano),
		statusColor, param.StatusCode, resetColor,
		param.Latency,
		param.ClientIP,
		methodColor, param.Method, resetColor,
		param.Path,
		param.ErrorMessage,
	)
}

// ServeAnUpload is a http request handler for upload a file.
// In URL parameter "filename" is mandatory.
// Example: curl.exe -X POST http://127.0.0.1:64000/upload?&Filename="sendfile.rar" -T .\sendfile.rar
func ServeAnUpload(c *gin.Context) {
	strSessionId, err := c.Cookie(liteimp.KeysessionID)
	if err != nil || strSessionId == "" { // no previous session. Only new files expected.

		//generate new cookie
		newsessionID := uuid.New().String()
		log.Println(LogMsg(c, fmt.Sprintf("starts a new session %s", newsessionID)))
		c.SetCookie(liteimp.KeysessionID, newsessionID, 300, "", "", false, true)

		// c holds session id in KeyValue pair
		c.Set(liteimp.KeysessionID, newsessionID)

		RequestedAnUpload(c, newsessionID) // recieves new files only

	} else {
		// load session state
		if state, found := clientsstates[strSessionId]; found {
			//c.Error(fmt.Errorf("session continue %s", strSessionId))
			if state.good {
				log.Println(LogMsg(c, fmt.Sprintf("continue upload")))

				// c holds session id in KeyValue pair
				c.Set(liteimp.KeysessionID, strSessionId)

				RequestedAnUploadContinueUpload(c, state) // continue upload
				return
			}

		}

		// stale state
		delete(clientsstates, strSessionId)
		c.SetCookie(liteimp.KeysessionID, "", 300, "", "", false, true)
		c.Set(liteimp.KeysessionID, "")

		c.JSON(http.StatusBadRequest, gin.H{"error": errSessionEnded.SetDetails("Requested session id %s is wrong.", strSessionId)})
		return

	}

}

// RequestedAnUploadContinueUpload continue to upload a non complete file.
// expectfromclient is a state of the file
func RequestedAnUploadContinueUpload(c *gin.Context, expectfromclient stateOfFileUpload) {
	name := expectfromclient.name
	storagepath := expectfromclient.path

	strSessionId := c.GetString(liteimp.KeysessionID)

	// Uses sync.Map to prevent uploading the same file in concurrents http handlers.
	// usedfiles is global for http server.
	_, loaded := usedfiles.LoadOrStore(name, true)
	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusConflict, gin.H{"error": errRequestedFileIsBusy.SetDetails("Requested filename is busy at the moment: %s", name)})
		return
	}

	// clears sync.Map after http.Handler func exits.
	defer usedfiles.Delete(name)

	// Creates reciever channel to hold read bytes in this connection.
	// Bytes from chReciever will be written to file.
	chReciever := make(chan []byte, 2)

	// Channel chWriteResult is used to send back result from WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)

	// Prevents another client update content of the file in between our requests.
	// Gets a struct with the file current size and state.
	whatIsInFile, err := fsdriver.MayUpload(expectfromclient.path, name)
	if err != nil {
		// Here err!=nil means upload is now allowed
		delete(clientsstates, strSessionId)

		c.Error(err)
		log.Println(LogMsg(c, fmt.Sprintf("upload is not allowed")))

		c.JSON(http.StatusForbidden, gin.H{"error": liteimp.ErrUploadIsNorAllowed.SetDetails("filename %s", name)})
		return

	}

	// This client request must have fromClient.Startoffset == whatIsInFile.Startoffset
	var fromClient liteimp.QueryParamsToContinueUpload

	// Client sends startoffset, count, filеname in URL parameters
	err = c.ShouldBindQuery(&fromClient)
	if err != nil {
		// Сlient didn't request with proper params.
		// We expecting URL parametres.
		jsonbytes, _ := json.MarshalIndent(&fromClient, "", " ")
		msgdetails := "" + string(jsonbytes)
		//
		c.SetCookie(liteimp.KeysessionID, "", 300, "", "", false, true) // clear cookie
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
		if errreciver != nil || writeresult.err != nil ||
			(whatIsInFile.Startoffset+writeresult.count) != whatIsInFile.FileSize {
			// server failed to write all the bytes,
			// OR reciever failed to recieve all the bytes
			// OR recieved bytea are not the exact end of file

			// update state of the file after failed upload
			whatIsInFile, err := fsdriver.MayUpload(expectfromclient.path, name)
			if err != nil {
				// Here err!=nil means upload is now allowed

				delete(clientsstates, strSessionId)

				c.Error(err)
				log.Println(LogMsg(c, fmt.Sprintf("upload is not allowed")))

				c.JSON(http.StatusForbidden, gin.H{"error": liteimp.ErrUploadIsNorAllowed.SetDetails("filename %s", name)})
				return

			}
			// This will trigger another request from client.
			// Client must send rest of the file.
			c.JSON(http.StatusConflict, *ConvertFileStateToJsonFileStatus(whatIsInFile))
			return
		}

		// SUCCESS!!!
		// Next check fact sha1 with expected sha1 if it was given.
		factsha1, err := GetFileSha1(storagepath, name)
		if err != nil {
			log.Println(LogMsg(c, fmt.Sprintf("Can't compute sha1 for the file %s. %s.", name, err)))
		}
		wantsha1 := whatIsInFile.Sha1
		if !bytes.Equal(wantsha1, emptysha1[:]) {
			// we may check correctness

			if !bytes.Equal(factsha1, wantsha1) {
				// sha1 differs!!!
				log.Println(LogMsg(c, fmt.Sprintf("check sha1 failed. want = %x, has = %x.", wantsha1, factsha1)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": errSha1CheckFailed.SetDetails("sha1 check failed: want = %x, has = %x", wantsha1, factsha1)})
				return
			}
		}

		// Rename journal file. Add string representaion of sha1 to the journal filename.
		namepart := fsdriver.CreatePartialFileName(name)
		namepartnew := filepath.Join(storagepath, fmt.Sprintf("%s.sha1-%x", namepart, factsha1))
		_ = os.Rename(filepath.Join(storagepath, namepart), filepath.Join(storagepath, namepartnew))

		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSeccessfullUpload})
		return
	}

	// Client made wrong request
	c.SetCookie(liteimp.KeysessionID, "", 300, "", "", false, true) // clear cookie
	delete(clientsstates, strSessionId)

	jsonbytes, _ := json.MarshalIndent(whatIsInFile, "", " ")
	msgdetails := "" + string(jsonbytes)
	c.JSON(http.StatusBadRequest, gin.H{"error": errServerExpectsRestOfTheFile.SetDetails("you need to specify URL parameters, %s", msgdetails)})
	return
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
		c.JSON(http.StatusBadRequest, gin.H{"error": errWrongURLParameters.SetDetails("Expected url parameter &filename")})
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

		delete(clientsstates, strSessionId)

		log.Println(LogMsg(c, fmt.Sprintf("upload is not allowed")))

		c.JSON(http.StatusForbidden, gin.H{"error": liteimp.ErrUploadIsNorAllowed.SetDetails("filename %s", name)})

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
	strsha1 := c.GetHeader("Sha1")

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
		var bytessha1 []byte
		if strsha1 != "" {
			bytessha1 = make([]byte, hex.DecodedLen(len(strsha1)))
			_, err := hex.Decode(bytessha1, []byte(strsha1))
			if err != nil {
				log.Println(LogMsg(c, fmt.Sprintf("string representation of sha1 %s is invalid", strsha1)))
			}
		}
		// next fill a header of journal file
		err := fsdriver.BeginNewPartialFile(storagepath, name, lcontent, bytessha1)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// next: recive and put to channel, read from this channel and write, wait for end of write operation
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
		// next check sha1

		factsha1, err := GetFileSha1(storagepath, name)
		if bytessha1 != nil {
			// we may check correctness

			if !bytes.Equal(factsha1, bytessha1) {
				// sha1 differs!!!
				log.Println(LogMsg(c, fmt.Sprintf("check sha1 failed. want = %x, has = %x.", bytessha1, factsha1)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": errSha1CheckFailed.SetDetails("sha1 check failed: want = %x, has = %x", bytessha1, factsha1)})
				return
			}
		}

		//SUCCESS!!!
		// Rename journal file. Add string representaion of sha1 to the journal filename.
		namepart := fsdriver.CreatePartialFileName(name)
		namepartnew := filepath.Join(storagepath, fmt.Sprintf("%s.sha1-%x", namepart, factsha1))

		_ = os.Rename(filepath.Join(storagepath, namepart), filepath.Join(storagepath, namepartnew))

		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSeccessfullUpload})
		return

	}

	// Here we are when uploaded file already exist.
	// We respond with a json with expected offsett and file size.

	c.JSON(http.StatusConflict, *ConvertFileStateToJsonFileStatus(whatIsInFile))

	// We expect the client to do one more request with the rest of the file.
	// Session State saved in a map, session key (liteimp.KeysessionID) is in cookie.

}

// WaitForWriteChanResult wait for the end of write operation.
// Returns: ok == true when there was written expected count of bytes.
// rename WaitForWriteToFinish
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
	return // named

}

func GetPathWhereToStore() string {
	return "./" //TODO(zavla): change per user?
}

func GetFileSha1(storagepath, name string) ([]byte, error) {
	var ret []byte
	f, err := os.Open(filepath.Join(storagepath, name))
	if err != nil {
		return ret, err
	}
	defer f.Close()

	h := sha1.New()
	_, err = io.Copy(h, f)
	if err != nil {
		return ret, err
	}
	return h.Sum(nil), nil
}

// To send a new file:
// curl.exe -X GET http://127.0.0.1:64000/upload?"&"Filename="sendfile.rar" -T .\sendfile.rar
