package uploadserver

import (
	"Upload/fsdriver"
	"Upload/liteimp"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	Error "upload/errstr"

	//_ "time"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

//-----has own errors
var (
//errNoError               = *errstr.NewError("uploadserver", 0, "Success.")
//errCantStartDownload     = *errstr.NewError("uploadserver", 2, "Can not start download.")
//errFileAlreadyExists     = *errstr.NewError("uploadserver", 3, "File is already fully downloaded.")
//errCantDeletePartialFile = *errstr.NewError("uploadserver", 4, "Cant delete partial file.")
//--
//errServerExpectsRestOfTheFile    = *errstr.NewError("uploadserver", 10, "Server expects rest of the file.")
//errConnectionReadError           = *errstr.NewError("uploadserver", 11, "Connection read error.")
//errWrongURLParameters            = *errstr.NewError("uploadserver", 12, "Query URL must set &filename parameter")
//errRequestedFileIsBusy           = *errstr.NewError("uploadserver", 13, "Requested filename is busy at the moment")
//errUnexpectedFuncReturn          = *errstr.NewError("uploadserver", 14, "Unexpected return from func.")
//errContentHeaderRequired         = *errstr.NewError("uploadserver", 15, "Content-Length header required for your new file")
//errServerFailToWriteAllbytes     = *errstr.NewError("uploadserver", 16, "Server failed to write all the bytes.")
//errClientRequestShouldBindToJson = *errstr.NewError("uploadserver", 17, "Client request should bind to json.")
//errSessionEnded                  = *errstr.NewError("uploadserver", 18, "Session has ended.")
//errWrongFuncParameters           = *errstr.NewError("uploadserver", 19, "Wrong func parameters.")
//errSha1CheckFailed               = *errstr.NewError("uploadserver", 20, "Sha1 check failed.")
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

// Storageroot holds path to file store for this instance.
// Must be absolute.
var Storageroot string

// RunningFromDir used to access html templates
var RunningFromDir string

const constrecieveblocklen = (1 << 16) - 1

// const bindToAddress = "127.0.0.1:64000"

type writeresult struct {
	count    int64
	err      error
	slerrors []error
}

// recieveAndSendToChan reads blocks from io.ReaderCloser until io.EOF or timeout.
// Expects large Gb files from http.Request.Boby.
// Sends bytes to chReciever.
// Suppose to work within a thread that reads connection.
// Exits when error or EOF.
func recieveAndSendToChan(c io.ReadCloser, chReciever chan []byte) error {
	const op = "uploadserver.recieveAndSendToChan()"
	// timeout is set via http.Server{Timeout...}
	defer close(chReciever)

	blocklen := 3 * constrecieveblocklen
	i := 0
	for { // endless recieve loop

		b := make([]byte, blocklen) // must allocate for every read or else reads will overwrite previous b
		n, err := c.Read(b)         // usually reads Request.Body
		if err != nil && err != io.EOF {

			return Error.E(op, err, errConnectionReadError, 0, "") // or timeout?
		}

		if n > 0 {

			chReciever <- b[:n] // buffered chReciever
		}
		if err == io.EOF {
			return nil // success reading
		}

		i++
	}

}

// consumeSourceChannel runs in background as a goroutine. Waits for input bytes in input chan.
// Adds bytes[] to a file, using transaction log file.
// Closes files at the return.
func consumeSourceChannel(
	chSource chan []byte,
	chResult chan<- writeresult,
	storagepath, name string,
	destination fsdriver.JournalRecord) {

	// to begin open destination files
	namelog := fsdriver.GetPartialJournalFileName(name)
	ver, wp, wa, errp, erra := fsdriver.OpenTwoCorrespondentFiles(storagepath, name, namelog)
	// wp = transaction log file
	// wa = actual file
	if errp != nil {
		chResult <- writeresult{0, errp, nil}
		return
	}
	// (os.File).Close may return err, means disk failure after File System Driver got bytes into its memmory with successfull Write().
	// second (defered) close in case of panic.
	defer wp.Close()

	if erra != nil {
		chResult <- writeresult{0, erra, nil}
		return
	}
	// second (defered) close. It is safe to call Close twice on *os.File.
	defer wa.Close()

	// small blocks buffer - for small []byte from source
	smallbytes := make([]byte, constrecieveblocklen*3)
	// count of bytes in smallbytes
	smallbytestotal := 0

	//---
	closed := false
	nbyteswritten := int64(0) // return nbyteswritten to chResult
	for !closed {
		select {
		case b, ok := <-chSource: // chSource capacity is > 1. Case waits only when chSource is empty.

			if !ok { // ok==false means channel closed and len(b)==0

				closed = true
				var writeerr error
				var err error
				var successbytescount int64
				// on closed channel write smallbytes buffer first
				if smallbytestotal != 0 {

					debugprint("WRITES BYTES smallbytes[:%d] -> offset %d\n", smallbytestotal, destination.Startoffset)

					successbytescount, writeerr = fsdriver.AddBytesToFile(wa, wp, smallbytes[:smallbytestotal], ver, &destination) // ACTUAL WRITE

					nbyteswritten += successbytescount

				}
				// explicit Close
				slerrors, closeerr := closeFiles(wa, wp) //
				err = closeerr                           // Close error is the last error while working with file.
				if writeerr != nil {
					err = writeerr // unless there was a Write error
				}

				chResult <- writeresult{nbyteswritten, err, slerrors} // err==nil means no error
				return
			}

			// here channel is not empty
			if len(b) != 0 {
				addedtosmallbytes := false // flag if the current b is in smallbytes or stands alone.
				if len(b) < constrecieveblocklen {
					copy(smallbytes[smallbytestotal:], b)

					smallbytestotal += len(b)
					addedtosmallbytes = true
				}
				// if b is large for smallbytes but smallbytes not empty?

				if smallbytestotal > constrecieveblocklen || (!addedtosmallbytes && smallbytestotal != 0) {
					// smallbytes byffer is big enough to be written

					debugprint("WRITES BYTES smallbytes[:%d] -> offset %d\n", smallbytestotal, destination.Startoffset)

					successbytescount, err := fsdriver.AddBytesToFile(wa, wp, smallbytes[:smallbytestotal], ver, &destination) // ACTUAL WRITE
					smallbytestotal = 0
					nbyteswritten += successbytescount
					if err != nil {
						// disk failure in Write
						// explicit Close
						slerrors, _ := closeFiles(wa, wp) // ignore Close error because there was a Write error.
						// not sure how much File System Driver has written.
						// We need to reread existing file to see what it has inside.
						chResult <- writeresult{nbyteswritten, err, slerrors}
						return

					}

				}
				if !addedtosmallbytes { // this b is not in smallbytes buffer

					debugprint("WRITES BYTES b[:%d] -> offset %d\n", len(b), destination.Startoffset)

					successbytescount, err := fsdriver.AddBytesToFile(wa, wp, b, ver, &destination) // ACTUAL WRITE

					nbyteswritten += successbytescount
					if err != nil {
						// disk Write failure
						// explicit Close
						slerrors, _ := closeFiles(wa, wp) // ignore Close error because there was a Write error
						chResult <- writeresult{nbyteswritten, err, slerrors}
						return

					}
				}

			}
			// here we do next channel read , case b,ok <- chSource
		} //select
	} //for !closed (while input channel not closed)

	return // ends gourouting
}

// startWriteStartRecieveAndWait runs writing G, runs recieving G, and waits for them to complete.
// Analyses returns from both Gs.
// Returns error from reciver G and error from writer G.
func startWriteStartRecieveAndWait(c *gin.Context, cRequestBody io.ReadCloser,
	chReciever chan []byte,
	chWriteResult chan writeresult,
	dir, name string,
	whatwhere fsdriver.JournalRecord) (writeresult, error) {

	const op = "uploadserver.startWriteStartRecieveAndWait()"

	expectedcount := whatwhere.Count
	if expectedcount < 0 { // should never happen, why we expect more than filesize
		// check input params
		helpmessage := fmt.Sprintf("The file offset in your request is wrong: %d.", expectedcount)
		currerr := Error.E(op, nil, errWrongFuncParameters, Error.ErrKindInfoForUsers, helpmessage)
		return writeresult{
			count: 0,
			err:   currerr,
		}, currerr
	}

	// Starts goroutine in background for write operations for this connection.
	// writeChanTo will write while we are recieving.
	go consumeSourceChannel(chReciever, chWriteResult, dir, name, whatwhere)

	// Reciever works in current goroutine, sends bytes to chReciever.
	// Reciever may end with error, by timeout with error, or by EOF with nil error.
	// First wait: for end of recieve
	errRecieve := recieveAndSendToChan(cRequestBody, chReciever) // exits when error or EOF

	// here reciever has ended.

	// Second wait: for end of write, we wait for write goroutine to complete.
	writeresult, ok := waitForWriteToFinish(chWriteResult, expectedcount) // waits for the end of writing

	// here ok=true means we have written the whole file.
	if ok && expectedcount == writeresult.count {
		return writeresult, nil // SUCCESS, all bytes written
	}
	// server failed to write all the bytes
	// or reciever failed to recieve all the bytes
	return writeresult, errRecieve

}

// convertFileStateToJSONFileStatus converts struct FileState to a json representation with
// exported liteimp.JsonFileStatus.
func convertFileStateToJSONFileStatus(state fsdriver.FileState) *liteimp.JsonFileStatus {
	return &liteimp.JsonFileStatus{
		JsonResponse: liteimp.JsonResponse{
			Startoffset: state.Startoffset,
			Count:       state.FileSize,
		},
	}
}

type logLine struct {
	gin.LogFormatterParams
}

// logline mimics gin defaultLogger to format a log line.
func logline(c *gin.Context, msg string) (ret logLine) {
	ret = logLine{gin.LogFormatterParams{
		Request: c.Request,
		IsTerm:  false,
	},
	}
	ret.TimeStamp = time.Now()
	ret.ClientIP = c.ClientIP()
	ret.Path = c.Request.URL.Path + "?" + c.Request.URL.RawQuery
	strSessionID := c.GetString(liteimp.KeysessionID)
	ret.ErrorMessage = fmt.Sprintf("In session id %s: '%s'", strSessionID, msg)
	return // named
}

// String used to print msg to a log.out
func (param logLine) String() string {

	var statusColor, methodColor, resetColor string

	return fmt.Sprintf(" |%s %3d %s| %13v | %15s |%s %-7s %s %s| %s",
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
	const op = "uploadserver.ServeAnUpload()"
	strSessionID, err := c.Cookie(liteimp.KeysessionID)
	loginInURL := c.Param("login")
	_, exists := c.Get(gin.AuthUserKey)
	if loginInURL != "" && !exists {
		panic("Login in path exists but login is not authenticated.")
	}
	if err != nil || strSessionID == "" { // no previous session. Only new files expected.

		//generate new cookie
		newsessionID := uuid.New().String()
		log.Println(logline(c, fmt.Sprintf("starts a new session %s", newsessionID)))
		// httpOnly==true for cookie be unavailable for javascript api.
		// path=/upload. "/upload" should be in URL path for cookie to be sent.
		c.SetCookie(liteimp.KeysessionID, newsessionID, 300, "/upload", "", false, true)

		// c holds session id in KeyValue pair
		c.Set(liteimp.KeysessionID, newsessionID)

		requestedAnUpload(c, newsessionID) // recieves new files only

	} else {
		// load session state
		if state, found := clientsstates[strSessionID]; found {
			//c.Error(fmt.Errorf("session continue %s", strSessionId))
			if state.good {
				log.Println(logline(c, fmt.Sprintf("continue upload")))

				// c holds session id in KeyValue pair
				c.Set(liteimp.KeysessionID, strSessionID)

				requestedAnUploadContinueUpload(c, state) // continue upload
				return
			}

		}

		// stale state, we clear clientsstates map for this session
		delete(clientsstates, strSessionID)
		c.SetCookie(liteimp.KeysessionID, "", 300, "", "", false, true)
		// we set cookie in response
		c.Set(liteimp.KeysessionID, "")

		c.JSON(http.StatusBadRequest, gin.H{"error": Error.E(op, nil, errSessionEnded, Error.ErrKindInfoForUsers, strSessionID)})
		return

	}

}

// requestedAnUploadContinueUpload continues an upload of a partially uploaded (not complete) file.
// Expects in request from client a liteimp.QueryParamsToContinueUpload with data
// correspondend to server's state of the file.
func requestedAnUploadContinueUpload(c *gin.Context, expectfromclient stateOfFileUpload) {
	const op = "uploadserver.requestedAnUploadContinueUpload()"
	name := expectfromclient.name
	storagepath := expectfromclient.path

	strSessionID := c.GetString(liteimp.KeysessionID)

	// Uses sync.Map to prevent uploading the same file in concurrents http handlers.
	// usedfiles is global for http server.
	_, loaded := usedfiles.LoadOrStore(name, true)
	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusConflict, gin.H{"error": Error.E(op, nil, errRequestedFileIsBusy, Error.ErrKindInfoForUsers, name)})
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
		delete(clientsstates, strSessionID)

		c.Error(err)
		log.Println(logline(c, fmt.Sprintf("upload is not allowed")))

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
		helpmessage := "" + string(jsonbytes)
		//
		c.SetCookie(liteimp.KeysessionID, "", 300, "", "", false, true) // clear cookie
		c.JSON(http.StatusBadRequest, gin.H{"error": Error.E(op, err, errClientRequestShouldBindToJSON, 0, helpmessage)})
		return
	}

	if fromClient.Startoffset == whatIsInFile.Startoffset {
		// client sends propper rest of the file
		writeresult, errreciver := startWriteStartRecieveAndWait(c, c.Request.Body,
			chReciever,
			chWriteResult,
			storagepath,
			name,
			fsdriver.JournalRecord{Startoffset: fromClient.Startoffset, Count: fromClient.Count})
		if errreciver != nil || writeresult.err != nil ||
			(whatIsInFile.Startoffset+writeresult.count) != whatIsInFile.FileSize {
			// server failed to write all the bytes,
			// OR reciever failed to recieve all the bytes
			// OR recieved bytea are not the exact end of file

			// update state of the file after failed upload
			whatIsInFile, err := fsdriver.MayUpload(expectfromclient.path, name)
			if err != nil {
				// Here err!=nil means upload is now allowed

				delete(clientsstates, strSessionID)

				c.Error(err)
				log.Println(logline(c, fmt.Sprintf("upload is not allowed")))

				c.JSON(http.StatusForbidden, gin.H{"error": liteimp.ErrUploadIsNorAllowed.SetDetails("filename %s", name)})
				return

			}
			// This will trigger another request from client.
			// Client must send rest of the file.
			c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))
			return
		}

		// SUCCESS!!!
		// Next check fact sha1 with expected sha1 if it was given.
		factsha1, err := fsdriver.GetFileSha1(storagepath, name)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("Can't compute sha1 for the file %s. %s.", name, err)))
		}
		wantsha1 := whatIsInFile.Sha1
		if !bytes.Equal(wantsha1, emptysha1[:]) {
			// we may check correctness

			if !bytes.Equal(factsha1, wantsha1) {
				// sha1 differs!!!
				log.Println(logline(c, fmt.Sprintf("check sha1 failed. want = %x, has = %x.", wantsha1, factsha1)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": Error.E(op, nil, errSha1CheckFailed, Error.ErrKindInfoForUsers, "")})
				return
			}
		}

		// Rename journal file. Add string representaion of sha1 to the journal filename.
		namepart := fsdriver.GetPartialJournalFileName(name)
		namepartnew := getFinalNameOfJournalFile(namepart, factsha1)
		newabsfilename := filepath.Join(storagepath, namepartnew)

		err = os.Rename(filepath.Join(storagepath, namepart), newabsfilename)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("rename failed from %s to %s, %s", namepart, namepartnew, err)))
		}
		log.Println(logline(c, fmt.Sprintf("Successfull upload: %s", newabsfilename)))
		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSeccessfullUpload})
		return
	}

	// Client made wrong request
	c.SetCookie(liteimp.KeysessionID, "", 300, "", "", false, true) // clear cookie
	delete(clientsstates, strSessionID)

	jsonbytes, _ := json.MarshalIndent(whatIsInFile, "", " ")
	helpmessage := "" + string(jsonbytes)
	c.JSON(http.StatusBadRequest, gin.H{"error": Error.E(op, nil, errServerExpectsRestOfTheFile, Error.ErrKindInfoForUsers, helpmessage)})
	return
}

// requestedAnUpload checks request params and recieves a new file.
// If file already exists requestAnUpload responds with a json liteimp.JsonFileStatus.
// We expect the client to do one more request with the rest of the file.
func requestedAnUpload(c *gin.Context, strSessionID string) {
	const op = "uploadserver.requestedAnUpload()"
	var req liteimp.RequestForUpload
	err := c.ShouldBindQuery(&req)
	if err != nil {
		// no parameter &filename
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "Details": `curl.exe -X POST http://127.0.0.1:64000/upload?&filename="sendfile.rar" -T .\sendfile.rar`})
		return // http request ends, wrong URL.
	}
	if req.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": Error.E(op, nil, errWrongURLParameters, Error.ErrKindInfoForUsers, "Expected parameter &filename")})
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
		c.JSON(http.StatusConflict, gin.H{"error": Error.E(op, nil, errRequestedFileIsBusy, Error.ErrKindInfoForUsers, name)})
		return
	}
	defer usedfiles.Delete(name) // clears sync.Map after http.Handler func exits.

	storagepath := GetPathWhereToStore(c)
	// storagepath must exist
	err = os.MkdirAll(storagepath, 0700)
	if err != nil {
		log.Println(logline(c, fmt.Sprintf("Can't create directory: %s", err)))
		return
	}

	// Get a struct with the file current size and state.
	// File may be partially uploaded.
	whatIsInFile, err := fsdriver.MayUpload(storagepath, name)
	if err != nil {
		c.Error(err)

		delete(clientsstates, strSessionID)

		log.Println(logline(c, fmt.Sprintf("upload is not allowed")))

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
			c.JSON(http.StatusLengthRequired, gin.H{"error": Error.E(op, nil, errContentHeaderRequired, 0, "")})
			return // client should make a new request
		}
	}
	strsha1 := c.GetHeader("Sha1")

	// create a map to hold clients state.
	clientsstates = make(map[string]stateOfFileUpload)

	clientsstates[strSessionID] = stateOfFileUpload{
		good:       true,
		name:       name,
		path:       storagepath,
		filestatus: *convertFileStateToJSONFileStatus(whatIsInFile),
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
				log.Println(logline(c, fmt.Sprintf("string representation of sha1 %s is invalid", strsha1)))
			}
		}
		// next fill a header of journal file

		err := fsdriver.CreateNewPartialJournalFile(storagepath, name, lcontent, bytessha1)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// next: recive and put to channel, read from this channel and write, wait for end of write operation
		writeresult, errreciver := startWriteStartRecieveAndWait(c, c.Request.Body,
			chReciever,
			chWriteResult,
			storagepath,
			name,
			fsdriver.JournalRecord{Count: lcontent, Startoffset: 0}) // blocks current Goroutine

		if errreciver != nil || writeresult.err != nil { // reciver OR write failed
			// server failed to write all the bytes
			if errreciver != nil {
				c.Error(errreciver)
			} //log error
			if writeresult.err != nil {
				c.Error(writeresult.err)
			}
			// updqate client state
			ptrstate := clientsstates[strSessionID]
			ptrstate.filestatus.Startoffset = writeresult.count
			helpmessage := fmt.Sprintf("Server has written %d bytes, but expected number of bytes was %d bytes.", writeresult.count, lcontent)
			c.JSON(http.StatusExpectationFailed, gin.H{"error": Error.E(op, nil, errServerFailToWriteAllbytes, Error.ErrKindInfoForUsers, helpmessage)})
			return
		}
		// next check sha1

		factsha1, err := fsdriver.GetFileSha1(storagepath, name)
		if bytessha1 != nil {
			// we may check correctness

			if !bytes.Equal(factsha1, bytessha1) {
				// sha1 differs!!!
				log.Println(logline(c, fmt.Sprintf("check sha1 failed. want = %x, has = %x.", bytessha1, factsha1)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": Error.E(op, nil, errSha1CheckFailed, Error.ErrKindInfoForUsers, "")})
				return
			}
		}

		//SUCCESS!!!
		// Rename journal file. Add string representaion of sha1 to the journal filename.
		namepart := fsdriver.GetPartialJournalFileName(name)
		namepartnew := getFinalNameOfJournalFile(namepart, factsha1)
		newabsfilename := filepath.Join(storagepath, namepartnew)
		err = os.Rename(filepath.Join(storagepath, namepart), newabsfilename)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("rename failed from %s to %s, %s", namepart, namepartnew, err)))
		}
		log.Println(logline(c, fmt.Sprintf("Successfull upload: %s", newabsfilename)))
		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSeccessfullUpload})
		return

	}

	// Here we are when uploaded file already exist.
	// We respond with a json with expected offsett and file size.

	c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))

	// We expect the client to do one more request with the rest of the file.
	// Session State saved in a map, session key (liteimp.KeysessionID) is in cookie.

}

// waitForWriteToFinish waits for the end of write operation.
// Returns: ok == true when there was written an expected count of bytes.
func waitForWriteToFinish(chWriteResult chan writeresult, expectednbytes int64) (retwriteresult writeresult, ok bool) {
	const op = "uploadserver.waitForWriteToFinish()"
	// asks-waits chan for the result
	nbyteswritten := int64(0) // returns
	ok = false                // returns
	retwriteresult = writeresult{count: 0, err: Error.E(op, nil, errUnexpectedFuncReturn, 0, "")}

	// waits for value in channel chWriteResult or chWriteResult be closed
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

// GetPathWhereToStore returns a subdir of current user
func GetPathWhereToStore(c *gin.Context) string {
	username := c.GetString(gin.AuthUserKey)
	if username == "" {
		return Storageroot
	}
	return filepath.Join(Storageroot, filepath.Base(username))
}

// getFinalNameOfJournalFile used to rename journal file when upload successfully completes.
func getFinalNameOfJournalFile(namepart string, factsha1 []byte) string {
	nopartial := strings.Replace(namepart, ".partialinfo", "", 1)
	return fmt.Sprintf("%s.sha1-%x", nopartial, factsha1)
}

func closeFiles(wa, wp *os.File) ([]error, error) {
	var slerrors []error
	err1 := wa.Close()
	if err1 != nil {
		//log.Println(logline(c, fmt.Sprintf("writer can't close file %s, %s", wa.Name(), err1)))
		slerrors = append(slerrors, err1)
	}
	err2 := wp.Close()
	if err2 != nil {
		//log.Println(logline(c, fmt.Sprintf("write can't close file %s, %s", wp.Name(), err2)))
		slerrors = append(slerrors, err2)

	}
	if err1 != nil {
		return slerrors, err1
	}
	if err2 != nil {
		return slerrors, err2
	}
	return nil, nil
}

// To send a new file:
// curl.exe -X GET http://127.0.0.1:64000/upload?"&"Filename="sendfile.rar" -T .\sendfile.rar
