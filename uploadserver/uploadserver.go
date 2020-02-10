package uploadserver

import (
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
	"upload/fsdriver"
	"upload/liteimp"

	//_ "time"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// Config is a type that hold all the configuration of this service.
type Config struct {
	Logwriter io.Writer
	Configdir string
	// BindAddress is an address of this service
	BindAddress string
	// Storageroot holds path to file store for this instance.
	// Must be absolute.
	Storageroot string
	// RunningFromDir used to access html templates
	RunningFromDir string
	// ActionOnCompleteFile your action. Default is to move a journal file to .sha1 directory.
	ActionOnCompleteFile func(filename, journalfilename string) error
	// AllowAnonymousUse
	AllowAnonymousUse bool
}

// ConfigThisService for config
var ConfigThisService Config

// constrecieveblocklen is a block (number of bytes) to recieve or write.
// If we've recieved les then constrecieveblocklen bytes they are considered to be a small packet that doesn't go directly to disk.
const constrecieveblocklen = (1 << 16) - 1

// The size of buffered channel that holds recieved slices of bytes.
// Its a count of constrecieveblocklen blocks.
const constChRecieverBufferLen = 10

// As we read from wire in a loop, constCountBlocksToReadFromWire is a number of constrecieveblocklen blocks to read at one loop step.
const constCountBlocksToReadFromWire = 5

const constmaxpath = 32767 - 6000 // reserve space for the service storage root
const constmaxfilename = 260

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

	blocklen := constCountBlocksToReadFromWire * constrecieveblocklen
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
		// DEBUG!!!
		// time.Sleep(150 * time.Millisecond)

		if err == io.EOF {
			return nil // success reading
		}

		i++
	}

}

// consumeSourceChannel runs in background as a goroutine. Waits for input bytes in input channel.
// Adds bytes[] to a file using transaction log file.
// Closes files at the return.
func consumeSourceChannel(
	chSource chan []byte,
	chResult chan<- writeresult,
	storagepath, name string,
	destination fsdriver.JournalRecord) {

	// to begin open both destination files
	namelog := fsdriver.GetPartialJournalFileName(name)
	ver, wp, wa, errp, erra := fsdriver.OpenTwoCorrespondentFiles(storagepath, name, namelog)
	// wp = transaction log file
	// wa = actual file
	if errp != nil {
		chResult <- writeresult{0, errp, nil}
		return
	}
	// (os.File).Close may return err, means disk failure after File System Driver got bytes into its
	// memmory with successfull Write().
	// This is second (defered) close in case of panic.
	defer wp.Close()

	if erra != nil {
		chResult <- writeresult{0, erra, nil}
		return
	}
	// second (defered) call to Close(). It is safe to call Close() twice on *os.File.
	defer wa.Close()

	// small blocks buffer - this is a buffer for small []bytes from source
	smallbytes := make([]byte, constrecieveblocklen*3)
	// count of bytes in smallbytes
	smallbytestotal := 0

	// closed == true means input channel is closed
	closed := false
	nbyteswritten := int64(0) // returns nbyteswritten to chResult channel
	for !closed {
		select {
		case b, ok := <-chSource: // chSource capacity is > 1. This waits only when chSource is empty.

			if !ok { // ok==false means channel closed and len(b)==0

				closed = true
				var writeerr error
				var err error
				var successbytescount int64
				// on closed channel write smallbytes buffer first
				if smallbytestotal != 0 {
					// our small buffer has some bytes
					debugprint("WRITES BYTES smallbytes[:%d] -> offset %d\n", smallbytestotal, destination.Startoffset)
					// ACTUAL WRITE
					successbytescount, writeerr = fsdriver.AddBytesToFile(wa, wp, smallbytes[:smallbytestotal], ver, &destination)

					nbyteswritten += successbytescount

				}
				// explicit Close
				slerrors, closeerr := closeFiles(wa, wp)
				// On Close() error is the last error while working with file.
				// Unless there was a Write error.
				err = closeerr
				if writeerr != nil {
					err = writeerr
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

				if smallbytestotal > constrecieveblocklen ||
					(!addedtosmallbytes && smallbytestotal != 0) {

					// Here we have the smallbytes buffer already big enough to be written.

					debugprint("WRITES BYTES smallbytes[:%d] -> offset %d\n", smallbytestotal, destination.Startoffset)
					// ACTUAL WRITE
					successbytescount, err := fsdriver.AddBytesToFile(wa, wp, smallbytes[:smallbytestotal], ver, &destination)
					smallbytestotal = 0
					nbyteswritten += successbytescount
					if err != nil {
						// Disk failure while Write().
						// Make explicit Close().
						slerrors, _ := closeFiles(wa, wp) // ignore Close error because there was a Write error.
						// Here we are not sure how much File System Driver has written on disk.
						// We need to reread existing file to see what it has inside.
						chResult <- writeresult{nbyteswritten, err, slerrors}
						return

					}

				}
				if !addedtosmallbytes { // this b is not in smallbytes buffer

					debugprint("WRITES BYTES b[:%d] -> offset %d\n", len(b), destination.Startoffset)
					// ACTUAL WRITE
					successbytescount, err := fsdriver.AddBytesToFile(wa, wp, b, ver, &destination)

					nbyteswritten += successbytescount
					if err != nil {
						// Disk Write() failure.
						// Make explicit Close().
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

// logLine Has String() method to be witten to log.
type logLine struct {
	gin.LogFormatterParams
}

// logline mimics gin defaultLogger to format a log line.
func logline(c *gin.Context, msg string) (ret logLine) {
	ret = logLine{gin.LogFormatterParams{
		Request: c.Request,
		//IsTerm:  false,
	},
	}
	ret.TimeStamp = time.Now()
	ret.ClientIP = c.ClientIP()
	ret.Path = c.Request.URL.Path + "?" + c.Request.URL.RawQuery
	strSessionID := c.GetString(liteimp.KeysessionID)
	msgformat := `session %s: "%s"`
	if strSessionID != "" {
		msgformat = `%s "%s"` // presumably for new session messages
	}
	ret.ErrorMessage = fmt.Sprintf(msgformat, strSessionID, msg)
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
// Example:
// curl.exe -X POST http://127.0.0.1:64000/upload?&Filename="sendfile.rar" -T .\sendfile.rar
// or curl.exe -X POST http://127.0.0.1:64000/upload/usernamehere/?&Filename="sendfile.rar" -T .\sendfile.rar
func ServeAnUpload(c *gin.Context) {
	const op = "uploadserver.ServeAnUpload()"
	// client may supply a session ID in its cookies.
	// Session ID is given to one file upload session.
	strSessionID, errNoID := c.Cookie(liteimp.KeysessionID)

	loginInURL := c.Param("login")
	// c gin.Context may hold a user already.
	_, userChecked := c.Get(gin.AuthUserKey)

	if loginInURL != "" && !userChecked {
		log.Println(logline(c, fmt.Sprintf("context has no value with key %s", gin.AuthUserKey)))
		panic("Login in URL but login is not authenticated.")
	}
	// no body means no file, but we respond to client with X-provepeerhasrightpasswordhash
	lcontentstr := c.GetHeader("Content-Length")
	lcontent, err := strconv.ParseInt(lcontentstr, 10, 64)
	if lcontent == 0 || err != nil {
		c.JSON(http.StatusLengthRequired,
			gin.H{"error": Error.ToUser(op, errContentHeaderRequired, "Service expects a file in request body.").Error()})
		return // client should make a new request
	}

	if errNoID != nil || strSessionID == "" {
		// Here we have no previous session ID. Only new files expected from client.

		// Generate new cookie that represents session number for current file upload. New file means new ID.
		newsessionID := uuid.New().String()
		log.Println(logline(c, fmt.Sprintf("new session ID %s", newsessionID)))

		// Next we set store ID into cookie.
		// httpOnly==true for the cookie to be unavailable for javascript api.
		// path=="/upload" means "/upload" should be in URL path for this cookie to be sent to client.
		c.SetCookie(liteimp.KeysessionID, newsessionID, 300, "/upload", "", http.SameSiteStrictMode, false, true)

		// c holds in its Context a session ID in KeyValue pair
		c.Set(liteimp.KeysessionID, newsessionID)

		requestedAnUpload(c, newsessionID) // recieves new files only. Doesn't reqiere special client.

	} else {
		// load current client session state
		if state, found := clientsstates[strSessionID]; found {

			if state.good {
				log.Println(logline(c, fmt.Sprintf("continue upload")))

				// c holds session ID in KeyValue pair
				c.Set(liteimp.KeysessionID, strSessionID)

				requestedAnUploadContinueUpload(c, state) // continue upload
				return
			}

		}
		retErr := Error.ToUser(op, errSessionEnded, "session deleted "+strSessionID)
		log.Println(logline(c, retErr.Error()))
		// stale state, we clear clientsstates map for this session
		delete(clientsstates, strSessionID) // delete is no-op when key not found
		// we set cookie in response header. -1 == delete cookie now.
		c.SetCookie(liteimp.KeysessionID, "", -1, "", "", http.SameSiteStrictMode, false, true)
		// we set cookie in current context.
		c.Set(liteimp.KeysessionID, "")
		// a user remains Authanticated, only a session ID cookie is cleared.

		//TODO(zavla): HOW to use internationalization???

		c.JSON(http.StatusBadRequest,
			gin.H{"error": retErr.Error()})
		return

	}

}

// requestedAnUploadContinueUpload continues an upload of a partially uploaded (not complete) file.
// Expects in request from client a liteimp.QueryParamsToContinueUpload with data
// correspondend to server's state of the file.
func requestedAnUploadContinueUpload(c *gin.Context, savedstate stateOfFileUpload) {
	const op = "uploadserver.requestedAnUploadContinueUpload()"
	name := savedstate.name
	storagepath := savedstate.path
	// we extract session ID from context.
	strSessionID := c.GetString(liteimp.KeysessionID)

	// Uses sync.Map to prevent uploading the same file in concurrent http handlers.
	// usedfiles is global for http server.
	_, loaded := usedfiles.LoadOrStore(name, true) // equals to ReadOrStore
	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusConflict, gin.H{"error": Error.E(op, nil, errRequestedFileIsBusy, Error.ErrKindInfoForUsers, name)})
		return
	}
	// clears sync.Map after this func exits.
	defer usedfiles.Delete(name)

	// Creates reciever channel to hold read bytes in this connection.
	// Bytes from chReciever will be written to file.
	chReciever := make(chan []byte, constChRecieverBufferLen)

	// Channel chWriteResult is used to send back result from WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)

	// Prevents another client update content of the file in between our requests.
	// Gets a struct with the file current size and state.
	nameNotComplete := name + ".part"
	whatIsInFile, err := fsdriver.MayUpload(savedstate.path, name, nameNotComplete)
	if err != nil {
		// Here err!=nil means upload is now allowed
		delete(clientsstates, strSessionID)

		// c.Error(err)
		log.Println(logline(c, fmt.Sprintf("upload is not allowed %s", name)))

		c.JSON(http.StatusForbidden, gin.H{"error": Error.ToUser(op, liteimp.ErrUploadIsNotAllowed, name).Error()})
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
		helpmessage := "service expects from you JSON " + string(jsonbytes)
		//
		c.SetCookie(liteimp.KeysessionID, "", 300, "", "", http.SameSiteStrictMode, false, true) // clear cookie
		c.JSON(http.StatusBadRequest, gin.H{"error": Error.ToUser(op, errClientRequestShouldBindToJSON, helpmessage).Error()})
		return
	}

	if fromClient.Startoffset == whatIsInFile.Startoffset {
		// client sends propper rest of the file
		writeresult, errreciver := startWriteStartRecieveAndWait(c, c.Request.Body,
			chReciever,
			chWriteResult,
			storagepath,
			nameNotComplete,
			fsdriver.JournalRecord{Startoffset: fromClient.Startoffset, Count: fromClient.Count})
		if errreciver != nil || writeresult.err != nil ||
			(whatIsInFile.Startoffset+writeresult.count) != whatIsInFile.FileSize {
			// server failed to write all the bytes,
			// OR reciever failed to recieve all the bytes
			// OR recieved bytea are not the exact end of file

			// update state of the file after failed upload
			whatIsInFile, err := fsdriver.MayUpload(savedstate.path, name, nameNotComplete)
			if err != nil {
				// Here err!=nil means upload is now allowed

				delete(clientsstates, strSessionID)

				// c.Error(err)
				log.Println(logline(c, fmt.Sprintf("upload is not allowed %s", name)))

				c.JSON(http.StatusForbidden, gin.H{"error": Error.ToUser(op, liteimp.ErrUploadIsNotAllowed, name).Error()})
				return

			}
			// This will trigger another request from client.
			// Client must send rest of the file.
			c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))
			return
		}

		// SUCCESS!!!
		// Next check fact sha1 with expected sha1 if it was given.
		factsha1, err := fsdriver.GetFileSha1(storagepath, nameNotComplete)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("Can't compute sha1 for the file %s. %s.", nameNotComplete, err)))
		}
		wantsha1 := whatIsInFile.Sha1
		if !bytes.Equal(wantsha1, emptysha1[:]) {
			// we may check correctness

			if !bytes.Equal(factsha1, wantsha1) {
				// sha1 differs!!!
				log.Println(logline(c, fmt.Sprintf("check sha1 failed. want = %x, has = %x.", wantsha1, factsha1)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": Error.ToUser(op, errSha1CheckFailed, "A file is complete but SHA1 is incorrect. It's an error.").Error()})
				return
			}
		}

		// Rename journal file. Add string representaion of sha1 to the journal filename.

		err = eventOnSuccess(c, storagepath, name, nameNotComplete, factsha1)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("event 'onSuccess' failed: %s", err)))
		}
		log.Println(logline(c, fmt.Sprintf("successfull upload: %s", name)))

		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSuccessfullUpload})
		return
	}

	// Client made a request with wrong offset
	// This will trigger another request from client.
	// Client must send rest of the file.
	c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))

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
		c.JSON(http.StatusBadRequest,
			gin.H{"error": Error.ToUser(op, errWrongURLParameters, `Example: curl.exe -X POST http://127.0.0.1:64000/upload?&filename="sendfile.rar" -T .\sendfile.rar`).Error()})
		return // http request ends, wrong URL.
	}
	if req.Filename == "" {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": Error.ToUser(op, errWrongURLParameters, Error.I18text("Expected URL parameter &filename")).Error()})
		return
	}

	// A filename may be from another OS: c:\windows\filename, \\?\c:\windows, \filename, /filename, \\computer\dir\filename, /../../filename.
	// Validate user supplied input.
	if validatefilepath(req.Filename, constmaxpath) != nil {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": Error.ToUser(op, errPathError, Error.I18text(`you specified a wrong path in parameter 'filename' %s`, req.Filename)).Error()})
		return
	}
	fullpath := filepath.Clean(req.Filename) // work about ../../

	name := filepath.Base(fullpath) // drop the path part from user supplied input

	storagepath := GetPathWhereToStore(c)
	// storagepath must exist. mkdirAll will create all the path.
	err = os.MkdirAll(storagepath, 0700)
	if err != nil {
		log.Println(logline(c, fmt.Sprintf("can't create storage root directory: %s", err)))
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": Error.ToUser(op, Error.ErrFileIO, Error.I18text(`service can't create root storage directory.`)).Error()})
		return
	}
	username := c.GetString(gin.AuthUserKey) // used in sync.Map usedfiles.
	if username == "" {
		// anonymous users are not allowed to upload to a full path of the file.
		fullpath = name
	}

	// sync.Map to prevent uploading the same file in parallel.
	// usedfiles is global for this service.
	lockobject := filepath.Join(username, fullpath)
	_, loaded := usedfiles.LoadOrStore(lockobject, true) // equal to ReadOrStore
	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusConflict,
			gin.H{"error": Error.ToUser(op, errRequestedFileIsBusy, fullpath).Error()})
		return
	}
	defer usedfiles.Delete(lockobject) // clears sync.Map after this func exits.

	// Get a struct with the file current size and state.
	// File may be partially uploaded.
	// MayUpload returns info about existing file.
	nameNotComplete := name + ".part"
	whatIsInFile, err := fsdriver.MayUpload(storagepath, name, nameNotComplete)
	if err != nil {
		// c.Error(err)

		delete(clientsstates, strSessionID)

		log.Println(logline(c, fmt.Sprintf("upload is not allowed %s", lockobject)))

		c.JSON(http.StatusForbidden,
			gin.H{"error": Error.ToUser(op, liteimp.ErrUploadIsNotAllowed, fullpath).Error()})

		return

	}
	// here we allow upload!
	strsha1 := c.GetHeader("Sha1") // unnecessary file checksum.

	// create a map to hold clients state.
	clientsstates = make(map[string]stateOfFileUpload)

	clientsstates[strSessionID] = stateOfFileUpload{
		good:       true,
		name:       name,
		path:       storagepath,
		filestatus: *convertFileStateToJSONFileStatus(whatIsInFile),
	}

	// Expects from client a file length if this is a new file.
	// But client has passed the authorization, so we add him to the clientsstates map.
	// Client doesn't send file at once, it waits from server a httpDigestAuthentication.KeyProvePeerHasRightPasswordhash header.

	lcontentstr := c.GetHeader("Content-Length")
	lcontent := int64(0)
	if lcontentstr != "" {
		lcontent, err = strconv.ParseInt(lcontentstr, 10, 64)
		if lcontent == 0 || err != nil {
			c.JSON(http.StatusLengthRequired,
				gin.H{"error": Error.ToUser(op, errContentHeaderRequired, Error.I18text(`Content-Length header required`)).Error()})
			return // client should make a new request
		}
	}

	// Create recieve channel to write bytes for this connection.
	// chReciever expects bytes from io.Reader to be written to file.
	chReciever := make(chan []byte, 2)
	// chWriteResult is used to send result of WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)

	if whatIsInFile.Startoffset == 0 { // Startoffset == 0 means such file is not exist yet.
		// A new file to upload, no need for json in request.
		var sha1fromclient []byte
		if strsha1 != "" {
			sha1fromclient = make([]byte, hex.DecodedLen(len(strsha1)))
			_, err := hex.Decode(sha1fromclient, []byte(strsha1))
			if err != nil {
				log.Println(logline(c, fmt.Sprintf("string representation of sha1 %s is invalid", strsha1)))
			}
		}
		// next fill a header of journal file

		err := fsdriver.CreateNewPartialJournalFile(storagepath, nameNotComplete, lcontent, sha1fromclient)
		if err != nil {
			log.Println(logline(c, err.Error()))
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": Error.ToUser(op, errInternalServiceError, "").Error()})
			return
		}
		// MAIN work:
		// recieve bytes and put them to channel for write;
		// read from this write channel and write to disk;
		// wait for end of write operation.
		writeresult, errreciver := startWriteStartRecieveAndWait(c, c.Request.Body,
			chReciever,
			chWriteResult,
			storagepath,
			nameNotComplete,
			fsdriver.JournalRecord{Count: lcontent, Startoffset: 0}) // blocks current Goroutine

		if errreciver != nil || writeresult.err != nil { // reciver OR write failed
			// server failed to write all the bytes
			if errreciver != nil {
				log.Println(logline(c, errreciver.Error()))
				c.Error(errreciver)
			} //log error
			if writeresult.err != nil {
				log.Println(logline(c, writeresult.err.Error()))
				c.Error(writeresult.err)
			}
			// updqate client state
			ptrstate := clientsstates[strSessionID]
			ptrstate.filestatus.Startoffset = writeresult.count

			c.JSON(http.StatusExpectationFailed,
				gin.H{"error": Error.ToUser(op, errServerFailToWriteAllbytes, Error.I18text(`service has written %d bytes, but expected number of bytes was %d bytes.`, writeresult.count, lcontent)).Error()})
			return
		}

		// Next we try to check sha1.
		factsha1, err := fsdriver.GetFileSha1(storagepath, nameNotComplete)
		if sha1fromclient != nil {
			// There was a sha1 from client, we may check correctness now.

			if !bytes.Equal(factsha1, sha1fromclient) {
				// sha1 differs!!!
				// TODO(zavla): needs more sofisticated algorithm to repare file.
				log.Println(logline(c, fmt.Sprintf("check sha1 failed. want = %x, has = %x.", sha1fromclient, factsha1)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": Error.ToUser(op, errSha1CheckFailed, "").Error()})
				return
			}
		}

		//SUCCESS!!!
		err = eventOnSuccess(c, storagepath, name, nameNotComplete, factsha1)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("event 'onSuccess' failed: %s", err)))
		}
		log.Println(logline(c, fmt.Sprintf("successfull upload: %s", lockobject)))
		c.JSON(http.StatusAccepted, gin.H{"error": Error.ToUser(op, liteimp.ErrSuccessfullUpload, "").Error()})
		return

	}

	// Here we are when uploaded file already exist.
	// We respond with a json with expected offsett and file size.

	c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))

	// We expect the client to do one more request with the rest of the file.
	// Session State is saved in a map, session key (liteimp.KeysessionID) is in cookie.

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
	// may be only half a file was written
	// may be only half a file was recieved
	return // named

}

// GetPathWhereToStore returns a subdir for the current user.
func GetPathWhereToStore(c *gin.Context) string {
	username := c.GetString(gin.AuthUserKey)
	if username == "" {
		return ConfigThisService.Storageroot
	}
	return filepath.Join(ConfigThisService.Storageroot, filepath.Base(username))
}

// getFinalNameOfJournalFile return the journal file name when its upload successfully completes.
// e.x. abcd.partialinfo -> abcd.sha-XXXX... . XXXX is the hex of the actual file hash.
func getFinalNameOfJournalFile(namepart string, factsha1 []byte) string {
	nopartial := strings.Replace(namepart, ".partialinfo", "", 1)
	return fmt.Sprintf("%s.sha1-%x", nopartial, factsha1)
}

// closeFiles deals with errors while closing.
// Close() with an error means no guarantie for how much has been written.
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

// eventOnSuccess is called on successfull upload of the file with name 'name'.
// Currently it moves journal file to a .sha1 directory.
func eventOnSuccess(c *gin.Context, storagepath, name string, nameNotComplete string, factsha1 []byte) (err error) {
	journalName := fsdriver.GetPartialJournalFileName(nameNotComplete)
	newjournalName := fsdriver.GetPartialJournalFileName(name)
	err = nil
	if ConfigThisService.ActionOnCompleteFile == nil {
		// default action == move to .sha1
		journalNewName := getFinalNameOfJournalFile(newjournalName, factsha1)
		journalNewPath := storagepath + "/.sha1" // all journals we will store in a directory
		newabsfilename := filepath.Join(journalNewPath, journalNewName)
		// actual action on the journal file: journal is renamed and moved to .sha1 dir.
		mkerr := os.MkdirAll(journalNewPath, 0700) // makes .sha1 dir
		if mkerr != nil {
			log.Println(logline(c, fmt.Sprintf("mkdir failed %s: %s", journalNewPath, mkerr)))

		}
		// rename a journal file
		err = os.Rename(filepath.Join(storagepath, journalName), newabsfilename)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("rename failed from %s to %s: %s", journalName, journalNewName, err)))
		}
		// rename actual file
		err = os.Rename(filepath.Join(storagepath, nameNotComplete), filepath.Join(storagepath, name))
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("rename failed from %s to %s: %s", nameNotComplete, name, err)))
		}

	} else {
		// user supplied action
		err = ConfigThisService.ActionOnCompleteFile(name, journalName)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("user supplied ActionOnCompleteFile() on file %s failed with error: %s", journalName, err)))
		}
	}

	return //named
}

// validatefilepath allows "/path1/long path$~123/../filename.ext"
func validatefilepath(pathstr string, maxlen int) (err error) {
	const op = "uploadserver.validatefilepath()"
	err = nil
	if len(pathstr) > maxlen {
		return Error.New(op, nil, errPathError)
	}

	if strings.ContainsAny(pathstr, `<>:"|?*
	`) ||
		strings.Contains(pathstr, `\\`) ||
		strings.Contains(pathstr, `//`) {
		return Error.New(op, nil, errPathError)
	}
	// rare cases
	for _, r := range pathstr {
		if r <= 31 {
			return Error.New(op, nil, errPathError)

		}
	}
	return //named
}

// To send a new file:
// curl.exe -X GET http://127.0.0.1:64000/upload?"&"Filename="sendfile.rar" -T .\sendfile.rar
