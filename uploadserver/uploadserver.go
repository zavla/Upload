package uploadserver

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	Error "github.com/zavla/upload/errstr"
	"github.com/zavla/upload/fsdriver"
	"github.com/zavla/upload/liteimp"
	"github.com/zavla/upload/logins"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stateOfFileUpload struct {
	userquery
	good bool
	// name       string
	// path       string
	filestatus liteimp.JsonFileStatus
}

var clientsstates map[string]stateOfFileUpload

var usedfiles sync.Map
var emptysha1 [20]byte

type interfaceconfig struct {
	Listenon string
	CertFile string
	KeyFile  string
}

// Config is a type that hold all the configuration of this service.
type Config struct {
	Logwriter io.Writer
	Logfile   *os.File
	Configdir string
	// BindAddress is an address of this service
	BindAddress []string
	//BindAddress2 string
	IfConfigs map[string]interfaceconfig
	LoginsMap map[string]logins.Login
	// Storageroot holds path to file store for this instance.
	// Must be absolute.
	Storageroot string

	// RunningFromDir used to access html templates
	RunningFromDir string
	// ActionOnCompleteFile your action. Default is to move a journal file to .sha1 directory.
	ActionOnCompleteFile func(filename, journalfilename string) error
	// AllowAnonymousUse
	AllowAnonymousUse bool
	Certs             []tls.Certificate

	// Usepprof will show /debug/pprof/* URLS
	Usepprof bool
}

// ConfigThisService for config
var ConfigThisService Config

// The size of buffered channel that holds recieved slices of bytes.
// Its a count of constrecieveblocklen blocks.
const constChRecieverBufferLen = 10

const constmaxpath = 32767 - 6000 // reserve space for the service storage root

type writeresult struct {
	count    int64
	err      error
	slerrors []error
}

func stackPrintOnPanic(c *gin.Context, where string) {
	// on panic we will write to log file
	if err := recover(); err != nil {
		// no '\r' symbols in log line
		// TODO(zavla): make structured log file?
		log.Println(logline(c, fmt.Sprintf("PANIC: in %s\n, error: %s\n", where, err)))
		b := make([]byte, 2500) // enough buffer for stack trace text
		n := runtime.Stack(b, true)
		b = b[:n]
		// output stack trace to a log
		log.Println(logline(c, fmt.Sprintf("%d bytes of stack trace\n%s\nENDPANIC\r\n", n, string(b))))
	}
}

func loginsToMap(loginsstruct logins.Logins) map[string]logins.Login {
	ret := make(map[string]logins.Login)
	for _, l := range loginsstruct.Logins {
		ret[l.Login] = l
	}
	return ret
}

// UpdateMapOfLogins creates a new map of logins and read them from logins.json file.
func (config *Config) UpdateMapOfLogins() error {
	// reads logins passwords
	loginsstruct, err := logins.ReadLoginsJSON(filepath.Join(config.Configdir, "logins.json"))

	if err != nil {
		// if configdir is specified , a file logins.json must exist
		log.Printf("you specify a config directory, there must exist a logins.json file: %s\n", err)
		return os.ErrNotExist
	}

	config.LoginsMap = loginsToMap(loginsstruct)
	return nil
}
func existPemFiles(path string, bindAddress string) bool {

	ipS := strings.Split(bindAddress, ":")[0]
	certFile := filepath.Join(path, ipS+".pem")
	keyFile := filepath.Join(path, ipS+"-key.pem")
	_, errCertPub := os.Stat(certFile)
	_, errCertKey := os.Stat(keyFile)
	if os.IsNotExist(errCertPub) || os.IsNotExist(errCertKey) {
		return false
	}
	return true
}

// InitInterfacesConfigs is used to initialize Config.
func (config *Config) InitInterfacesConfigs() {
	if config.IfConfigs == nil {
		config.IfConfigs = make(map[string]interfaceconfig, 2)
	}
	for _, v := range config.BindAddress {
		config.IfConfigs[v] = interfaceconfig{Listenon: v}
	}

}

// FilenamefromNetInterface links address with filename.
func (config *Config) FilenamefromNetInterface(netinterface string) string {

	if pos := strings.IndexAny(netinterface, ":"); pos != -1 {
		return netinterface[:pos]
	}
	return strings.ReplaceAll(netinterface, ":", "_")
}

// UpdateInterfacesConfigs creates configurations for every interface the service is listenning to.
// It checks if certificates files for every interface exists.
func (config *Config) UpdateInterfacesConfigs(selectinterface string) error {
	//var tlsConfig *tls.Config
	if config.IfConfigs == nil {
		config.IfConfigs = make(map[string]interfaceconfig, 2)
	}
	for _, v := range config.BindAddress {
		if selectinterface != "" && selectinterface != v {
			continue // update only a selected interface
		}
		ipS1 := strings.Split(v, ":")[0]
		if !existPemFiles(config.Configdir, ipS1) {

			// allow go routine to exit
			return os.ErrNotExist
		}
		certFile := filepath.Join(config.Configdir, ipS1+".pem")
		keyFile := filepath.Join(config.Configdir, ipS1+"-key.pem")
		config.IfConfigs[v] = interfaceconfig{
			Listenon: v,
			CertFile: certFile,
			KeyFile:  keyFile,
		}
	}

	return nil
}

type bufferConfig struct {
	Multiplicity int
	BlocksCount  int
}

// fillRecieverChannel reads blocks from io.ReaderCloser until io.EOF or timeout.
// Expects large Gb files from http.Request.Boby.
// Sends bytes to chReciever.
// Suppose to work within a thread that reads connection.
// Exits when error or EOF.
func fillRecieverChannel(c io.ReadCloser,
	chReciever chan []byte,
	done <-chan struct{},
	bufConfig bufferConfig,
) error {
	const op = "uploadserver.recieveAndSendToChan()"
	// timeout is set via http.Server{Timeout...}
	defer func() {

		close(chReciever)
	}()

	nbytessent := int64(0)

	// this makes sure bigBufferCap is bigger then bufConfig.SizeOfRecieverBuffer
	bigBufferCap := bufConfig.BlocksCount * bufConfig.Multiplicity
	bigBufLen := 0

	// bigBuffer escapes
	bigBuffer := make([]byte, bigBufferCap) // allocate

	// b is a buffer for current read. there may be many reads. b is noescape.
	b := make([]byte, bufConfig.Multiplicity)

	i := 0

	for { // endless recieve loop

		n, err := c.Read(b) // usually reads Request.Body
		// looks like n is mostly always 4096. tcp segment size?

		if err != nil && err != io.EOF {

			return Error.E(op, err, errConnectionReadError, 0, "") // or timeout?
		}
		if n > 0 {

			freespace := bigBufferCap - bigBufLen
			ncopied := 0

			// if bigBuffer is not full
			if freespace > 0 {
				nallowed := n
				if freespace < n {
					nallowed = freespace
				}

				ncopied = copy(bigBuffer[bigBufLen:], b[:nallowed])
				bigBufLen += ncopied
				freespace -= ncopied
			}
			if freespace == 0 {

				bigBufferEscapes := make([]byte, bigBufLen)   // allocate because it goes into another goroutine
				copy(bigBufferEscapes, bigBuffer[:bigBufLen]) // copy

				// if write to disk in neigbour goroutine failed that goroutine closes chReciever
				// and allows us to know there is no need to recieve more from connection.
				select {
				case <-done:
					return Error.E(op, nil, 0, 0, "chReciever is ordered to close")
				case chReciever <- bigBufferEscapes[:bigBufLen]: // buffered chReciever
					nbytessent += int64(bigBufLen)
				}

				bigBufLen = 0
				leftinb := len(b) - ncopied
				if leftinb > 0 {
					// if in b left something
					ncopied2 := copy(bigBuffer, b[ncopied:n])
					bigBufLen = ncopied2
				}
			}

		}
		// DEBUG!!! //time.Sleep(2 * time.Millisecond)

		if err == io.EOF {
			if bigBufLen > 0 {
				select {
				case <-done:
				case chReciever <- bigBuffer[:bigBufLen]:
					nbytessent += int64(bigBufLen)
				}
			}

			Debugprint("in fillRecieverChannel() nbytessent = %d", nbytessent)
			return nil // success reading
		}

		i++
	}

}

// consumeSourceChannel runs in background as a goroutine. Waits for input bytes in input channel.
// Adds bytes[] to a file using transaction log file.
// Closes files at the return.
func consumeSourceChannel(
	c *gin.Context,
	chSource chan []byte,
	chResult chan<- writeresult,
	storagepath, name string,
	destination fsdriver.JournalRecord,
	done chan struct{}) {

	const op = "uploadserver.consumeSourceChannel"

	defer stackPrintOnPanic(c, op)

	// to begin open both destination files: the journal file and the actual file
	namelog := fsdriver.GetPartialJournalFileName(name)
	ver, wp, wa, errp, erra := fsdriver.OpenTwoCorrespondentFiles(storagepath, name, namelog)
	// wp = transaction log file
	// wa = actual file
	if errp != nil {
		// files were not opened
		chResult <- writeresult{0, errp, nil}
		return
	}
	// (os.File).Close may return err, that means disk failure after File System Driver got bytes into its
	// memmory with successfull Write().
	// This is second (defered) close for the case of panic.
	defer wp.Close()

	if erra != nil {
		wp.Close() // wp was opened, close it
		// wa was not opened
		chResult <- writeresult{0, Error.E(op, erra, 0, Error.ErrUseKindFromBaseError, "from fsdriver.OpenTwoCorrespondentFiles"), nil}
		return
	}
	// second (defered) call to Close(). It is safe to call Close() twice on *os.File.
	defer wa.Close()

	// make sure file size equals to destination.startoffset
	wastat, err := wa.Stat()
	if err != nil {
		// some error, even can't get stats of a file
		closeFiles(wa, wp)
		chResult <- writeresult{0, err, nil}
		return
	}
	if wastat.Size() != destination.Startoffset {
		closeFiles(wa, wp)
		chResult <- writeresult{0, errors.New("startoffset not equal to existing file size"), nil}
		return
	}
	nextWrite := int64(1000000)
	nbyteswritten := int64(0) // returns nbyteswritten to chResult channel
	for b := range chSource {

		// ACTUAL WRITE
		successbytescount, err := fsdriver.AddBytesToFile(wa, wp, b, ver, &destination)

		nbyteswritten += successbytescount

		if nbyteswritten > nextWrite {
			nextWrite *= 2
			errp = wp.Sync() // journal file sync
			erra = wa.Sync() // actual file sync

		}

		if err != nil || errp != nil || erra != nil {
			// Here we have Disk failure while Write().
			// Make explicit Close(), close receiver's 'done' channel.

			close(done) // indicate to receiver give up receiving

			if err != nil {
				log.Println(logline(c, fmt.Sprintf("disk error in AddBytesToFile(), err=%s", err)))
			}
			if errp != nil {
				log.Println(logline(c, fmt.Sprintf("disk error in AddBytesToFile(), errp=%s", errp)))
			}
			if erra != nil {
				log.Println(logline(c, fmt.Sprintf("disk error in AddBytesToFile(), erra=%s", erra)))
			}

			slerrors := closeFiles(wa, wp)
			// Here we are not sure how much File System Driver has written on disk.
			// We need to reread existing file to see what it has inside.
			chResult <- writeresult{nbyteswritten, err, slerrors}
			return

		}

	}

	Debugprint("in consumeSourceChannel() nbyteswritten = %d\n", nbyteswritten)

	// do not interrupt this thread
	runtime.LockOSThread() // sometime os.Rename can't rename files because syncing is done in another thread than the rest of this func.

	errp = wp.Sync() // journal file sync
	erra = wa.Sync() // actual file sync
	if errp != nil {
		log.Println(logline(c, fmt.Sprintf("disk error in AddBytesToFile(), last Sync(), errp=%s", errp)))
	}
	if erra != nil {
		log.Println(logline(c, fmt.Sprintf("disk error in AddBytesToFile(), last Sync(), erra=%s", erra)))
	}

	slerrors := closeFiles(wa, wp)

	chResult <- writeresult{nbyteswritten, nil, slerrors}
	runtime.UnlockOSThread()
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

	defer stackPrintOnPanic(c, op)

	expectedcount := whatwhere.Count
	if expectedcount < 0 { // should never happen, why we expect more than filesize
		// check input params
		helpmessage := fmt.Sprintf("The file offset in your request is wrong: %d.", expectedcount)
		currerr := Error.E(op, nil, errWrongFuncParameters, Error.ErrKindInfoForUsers, helpmessage)
		log.Println(logline(c, currerr.Error()))
		return writeresult{
			count: 0,
			err:   currerr,
		}, currerr
	}

	// done will be closed be writing goroutine as an indication of writing failure.
	done := make(chan struct{})

	// Starts goroutine in background for write operations for this connection.
	// writeChanTo will write while we are recieving.
	go consumeSourceChannel(c, chReciever, chWriteResult, dir, name, whatwhere, done)

	bufferProperties := bufferConfig{Multiplicity: 65535, BlocksCount: 1}

	// Reciever works in current goroutine, sends bytes to chReciever.
	// Reciever may end with error, by timeout with error, or by EOF with nil error.
	// First wait: for end of recieve
	errRecieve := fillRecieverChannel(cRequestBody, chReciever, done, bufferProperties) // exits when error or EOF

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

// logline creates gin.LogFromatterParams.
// logline.String() prints.
func logline(c *gin.Context, msg string) (ret logLine) {
	ret = logLine{
		gin.LogFormatterParams{
			Request: c.Request,
		},
	}
	retQueryUnEsc, err := url.QueryUnescape(c.Request.URL.RawQuery)
	if err != nil {
		retQueryUnEsc = c.Request.URL.RawQuery
	}
	ret.TimeStamp = time.Now()
	ret.ClientIP = c.ClientIP()
	question := "?"
	if retQueryUnEsc == "" {
		question = ""
	}
	ret.Path = c.Request.URL.Path + question + retQueryUnEsc
	//strSessionID := c.GetString(litesimp.KeysessionID)
	// msgformat := `session %s: "%s"`
	// ret.ErrorMessage = fmt.Sprintf(msgformat, strSessionID, msg)
	ret.ErrorMessage = msg
	return // named
}

// String used to print msg to a log.out
func (param logLine) String() string {

	return fmt.Sprintf("|%3d| %11v| %13s|%4s| %s| %s", // no "\n"
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
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
	strSessionID, errNoSessionIDCookie := c.Cookie(liteimp.KeysessionID)

	loginInURL := c.Param("login")
	// c gin.Context may hold a user already.
	_, userChecked := c.Get(gin.AuthUserKey)

	// TODO(zavla): do not allow anonymous uploads anymore
	// TODO(zavla): allow addition of config lines with new DBs
	if loginInURL != "" && !userChecked {
		log.Println(logline(c, fmt.Sprintf("login in URL has login part but this login is not authenticated. context has no value with key %s", gin.AuthUserKey)))
		panic("login in URL has login part but this login is not authenticated.")
	}
	// no body means no file, but we respond to client with X-ProvePeerHasTheRightPasswordHash
	lcontentstr := c.GetHeader("Content-Length")
	lcontent, err := strconv.ParseInt(lcontentstr, 10, 64)
	if err != nil {
		lcontent = 0
	}
	// if lcontent == 0 || err != nil {
	// 	c.JSON(http.StatusLengthRequired,
	// 		gin.H{"error": Error.ToUser(op, errContentHeaderRequired, "Service expects a file in request body.").Error()})
	// 	return // client should make a new request
	// }

	// Here a user is when it has logged in.

	if errNoSessionIDCookie != nil || strSessionID == "" || lcontent == 0 {
		// Here we have no previous session ID. Only new files expected from client.
		// This response also sends a X-ProvePeerHasTheRightPasswordHash prove to the client.

		userquery, err := getUserquery(c)
		if err != nil {
			return // we dont create a session
		}

		// sync.Map to prevent uploading the same file in parallel.
		// usedfiles is global for this service.
		lockobject := filepath.Join(userquery.username, userquery.fullpath)
		_, loaded := usedfiles.LoadOrStore(lockobject, true) // equal to ReadOrStore

		if loaded {
			// file is already busy at the moment
			c.JSON(http.StatusForbidden,
				gin.H{"error": Error.ToUser(op, errRequestedFileIsBusy, userquery.fullpath).Error()})
			return
		}
		defer usedfiles.Delete(lockobject) // clears sync.Map after this func exits.

		// Get a struct with the file current size and state.
		// File may be partially uploaded.
		// MayUpload returns info about existing file.

		whatIsInFile, err := fsdriver.MayUpload(userquery.storagepath, userquery.name, userquery.nameNotComplete)
		if err != nil {

			if fsdriver.MayRepare(err) {
				ok, errComp := fsdriver.CompareSha1(userquery.storagepath, userquery.nameNotComplete, whatIsInFile.Sha1, emptysha1[:])
				if errComp == nil && ok {
					// on success event
					err = eventOnSuccess(c, userquery.storagepath, userquery.name, userquery.nameNotComplete, whatIsInFile.Sha1)
					if err != nil {
						log.Println(logline(c, fmt.Sprintf("event 'onSuccess' failed: %s", err)))
					}

				} else {
					log.Println(logline(c, fmt.Sprintf("SHA1 BAD, expected SHA1 %x, error: %s", whatIsInFile.Sha1, err)))

				}

			}

			log.Println(logline(c, fmt.Sprintf("upload is not allowed %s", lockobject)))

			c.JSON(http.StatusForbidden,
				gin.H{"error": Error.ToUser(op, liteimp.ErrUploadIsNotAllowed, userquery.fullpath).Error()})

			return

		}
		// here we allow upload!

		// We set a NEW cookie , next request from this client will come with this session cookie.
		// Generate new cookie that represents session number for current file upload. New file means new ID.
		newsessionID := uuid.New().String()
		//log.Println(logline(c, fmt.Sprintf("new session ID %s", newsessionID)))

		// Next we set store ID into cookie.
		// httpOnly==true for the cookie to be unavailable for javascript api.
		// path=="/upload" means "/upload" should be in URL path for this cookie to be sent to client.
		// TODO(zavla): 300 => 5000 ?>?. what is SameSiteStrictMode, read https://tools.ietf.org/html/draft-ietf-httpbis-cookie-same-site-00
		c.SetCookie(liteimp.KeysessionID, newsessionID, 3600*8, "/upload", "", true, true)

		// c holds in its Context a session ID in KeyValue pair
		c.Set(liteimp.KeysessionID, newsessionID)

		// Fill a package level variable, a map, to hold clients' state.
		clientsstates = make(map[string]stateOfFileUpload)

		clientsstates[newsessionID] = stateOfFileUpload{
			userquery: userquery,
			good:      true,
			// name:       name,
			// path:       storagepath,
			filestatus: *convertFileStateToJSONFileStatus(whatIsInFile),
		}
		c.Request.Body.Close() // try to free a connection because client may be sending a big file?
		c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))

	} else { // a client has send a session cookie
		// lets find current client session state by session cookie
		if state, found := clientsstates[strSessionID]; found {

			if state.good {
				//log.Println(logline(c, fmt.Sprintf("continue upload")))

				// c holds session ID in KeyValue pair
				c.Set(liteimp.KeysessionID, strSessionID)

				requestedAnUploadContinueUpload(c, state, lcontent) // continue upload
			}
			return

		}

		// next delete a client's session
		retErr := Error.ToUser(op, errSessionEnded, "now such session "+strSessionID)
		log.Println(logline(c, retErr.Error()))
		// we set cookie in response header. -1 == delete cookie now.
		c.SetCookie(liteimp.KeysessionID, "", -1, "/upload", "", true, true)

		// we set key/value pair in current context.
		c.Set(liteimp.KeysessionID, "")
		// a user remains Authanticated, only a session ID cookie is cleared.

		c.JSON(http.StatusBadRequest,
			gin.H{"error": retErr.Error()})
		return

	}

}

// requestedAnUploadContinueUpload continues an upload of a partially uploaded (not complete) file.
// Expects in request from client a liteimp.QueryParamsToContinueUpload with data
// correspondend to server's state of the file.
func requestedAnUploadContinueUpload(c *gin.Context, savedstate stateOfFileUpload, lcontent int64) {
	const op = "uploadserver.requestedAnUploadContinueUpload()"
	//name := savedstate.name
	//storagepath := savedstate.storagepath
	// we extract session ID from context.
	strSessionID := c.GetString(liteimp.KeysessionID)

	// Uses sync.Map to prevent uploading the same file in concurrent http handlers.
	// usedfiles is global for http server.
	lockobject := filepath.Join(savedstate.username, savedstate.fullpath)
	_, loaded := usedfiles.LoadOrStore(lockobject, true) // equals to ReadOrStore

	if loaded {
		// file is already busy at the moment
		c.JSON(http.StatusForbidden, gin.H{"error": Error.E(op, nil, errRequestedFileIsBusy, Error.ErrKindInfoForUsers, savedstate.name)})
		return
	}
	defer usedfiles.Delete(lockobject) // clears sync.Map after this func exits.

	// Creates reciever channel to hold read bytes in this connection.
	// Bytes from chReciever will be written to file.
	chReciever := make(chan []byte, constChRecieverBufferLen)

	// Channel chWriteResult is used to send back result from WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)

	// Prevents another client update content of the file in between our requests.
	// Gets a struct with the file current size and state.
	//nameNotComplete := name + ".part"
	whatIsInFile, err := fsdriver.MayUpload(savedstate.storagepath, savedstate.name, savedstate.nameNotComplete)
	if err != nil {
		// Here err!=nil means upload is now allowed
		delete(clientsstates, strSessionID)

		// c.Error(err)
		log.Println(logline(c, fmt.Sprintf("upload is not allowed %s", savedstate.name)))

		c.JSON(http.StatusForbidden, gin.H{"error": Error.ToUser(op, liteimp.ErrUploadIsNotAllowed, savedstate.name).Error()})
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
		c.SetCookie(liteimp.KeysessionID, "", -1, "/upload", "", true, true) // clear cookie
		c.JSON(http.StatusBadRequest, gin.H{"error": Error.ToUser(op, errClientRequestShouldBindToJSON, helpmessage).Error()})
		return
	}

	if whatIsInFile.Startoffset == 0 { // Startoffset == 0 means such file is not exist yet.
		filesize := fromClient.Count
		if filesize == 0 {
			filesize = lcontent
		}
		// for a new file the first client request defines the file size
		whatIsInFile.FileSize = filesize
		fromClient.Count = filesize

		// Expects from client a file length if this is a new file.
		// But client has passed the authorization, so we add him to the clientsstates map.
		// Client doesn't send file at once, it waits from server a httpDigestAuthentication.KeyProvePeerHasRightPasswordhash header.

		// A new file to upload, no need for json in request.
		var sha1fromclient []byte
		if savedstate.strsha1 != "" {
			sha1fromclient = make([]byte, hex.DecodedLen(len(savedstate.strsha1)))
			_, err := hex.Decode(sha1fromclient, []byte(savedstate.strsha1))
			if err != nil {
				log.Println(logline(c, fmt.Sprintf("string representation of sha1 %s is invalid", savedstate.strsha1)))
			}
		}
		whatIsInFile.Sha1 = sha1fromclient
		// next create and fill a header of new journal file

		err := fsdriver.CreateNewPartialJournalFile(savedstate.storagepath, savedstate.nameNotComplete, filesize, sha1fromclient)
		if err != nil {
			log.Println(logline(c, err.Error()))
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": Error.ToUser(op, errInternalServiceError, "").Error()})
			return
		}
	}

	if fromClient.Startoffset == whatIsInFile.Startoffset {

		// client sends propper rest of the file
		writeresult, errreciver := startWriteStartRecieveAndWait(c, c.Request.Body,
			chReciever,
			chWriteResult,
			savedstate.storagepath,
			savedstate.nameNotComplete,
			fsdriver.JournalRecord{Startoffset: fromClient.Startoffset, Count: fromClient.Count})
		if errreciver != nil || writeresult.err != nil ||
			(whatIsInFile.Startoffset+writeresult.count) != whatIsInFile.FileSize {

			// log.Println(logline(c, fmt.Sprintf("startWriteStartRecieveAndWait returned errreciver = %s", errreciver)))
			if writeresult.err != nil {
				// server failed to write all the bytes,
				// write problem goes to log
				log.Println(logline(c, fmt.Sprintf("service can't write, writeresult.err == %s", writeresult.err)))
				c.JSON(http.StatusInternalServerError, gin.H{"error": Error.ToUser(op, errInternalServiceError, "write error")})
				return
			}
			// OR reciever failed to recieve all the bytes
			// OR recieved bytea are not the exact end of file

			// update state of the file after failed upload
			whatIsInFile, err = fsdriver.MayUpload(savedstate.storagepath, savedstate.name, savedstate.nameNotComplete)
			if err != nil {
				// Here err!=nil means upload is now allowed

				delete(clientsstates, strSessionID)
				c.SetCookie(liteimp.KeysessionID, "", -1, "/upload", "", true, true) // clear cookie
				// c.Error(err)
				log.Println(logline(c, fmt.Sprintf("upload is not allowed %s", savedstate.name)))

				c.JSON(http.StatusForbidden, gin.H{"error": Error.ToUser(op, liteimp.ErrUploadIsNotAllowed, savedstate.name).Error()})
				return

			}
			// This will trigger another request from client.
			// Client must send rest of the file.
			c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))
			return
		}

		// SUCCESS!!!
		// Next check fact sha1 with expected sha1 if it was given.
		factsha1, err := fsdriver.GetFileSha1(savedstate.storagepath, savedstate.nameNotComplete)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("Error while computing SHA1 for the file %s, error %s.", savedstate.nameNotComplete, err)))
		}
		wantsha1 := whatIsInFile.Sha1
		if !bytes.Equal(wantsha1, emptysha1[:]) {
			// we may check correctness

			if !bytes.Equal(factsha1, wantsha1) {
				// sha1 differs!!!
				log.Println(logline(c, fmt.Sprintf("SHA1 failed, want = %x, has = %x, file %s", wantsha1, factsha1, savedstate.nameNotComplete)))
				c.JSON(http.StatusExpectationFailed, gin.H{"error": Error.ToUser(op, errSha1CheckFailed, "A file is complete but SHA1 is incorrect. It's an error.").Error()})
				return
			}
		}

		// Rename journal file. Add string representation of sha1 to the journal filename.

		err = eventOnSuccess(c, savedstate.storagepath, savedstate.name, savedstate.nameNotComplete, factsha1)
		if err != nil {
			log.Println(logline(c, fmt.Sprintf("event 'onSuccess' failed: %s", err)))
		}
		log.Println(logline(c, fmt.Sprintf("successfull upload: %s", savedstate.name)))

		c.JSON(http.StatusAccepted, gin.H{"error": liteimp.ErrSuccessfullUpload})
		return
	}

	// Client made a request with wrong offset
	// This will trigger another request from client.
	// Client must send rest of the file.

	c.JSON(http.StatusConflict, *convertFileStateToJSONFileStatus(whatIsInFile))

}

type userquery struct {
	fullpath        string
	name            string
	username        string
	storagepath     string
	strsha1         string
	nameNotComplete string
}

func getUserquery(c *gin.Context) (userquery, error) {
	const op = "uploadserver.firstActionOnRequest()"
	errStopwork := errors.New("StopWork")

	var req liteimp.RequestForUpload
	err := c.ShouldBindQuery(&req)
	if err != nil {
		// no parameter &filename
		c.JSON(http.StatusBadRequest,
			gin.H{"error": Error.ToUser(op, errWrongURLParameters, `Example: curl.exe -X POST http://127.0.0.1:64000/upload?&filename="sendfile.rar" -T .\sendfile.rar`).Error()})
		return userquery{}, errStopwork // http request ends, wrong URL.
	}
	if req.Filename == "" {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": Error.ToUser(op, errWrongURLParameters, Error.I18text("Expected URL parameter &filename")).Error()})
		return userquery{}, errStopwork
	}

	// A filename may be from another OS: c:\windows\filename, \\?\c:\windows, \filename, /filename, \\computer\dir\filename, /../../filename.
	// Validate user supplied input.
	if validatefilepath(req.Filename, constmaxpath) != nil {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": Error.ToUser(op, errPathError, Error.I18text(`you specified a wrong path in parameter 'filename' %s`, req.Filename)).Error()})
		return userquery{}, errStopwork
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
		return userquery{}, errStopwork
	}
	username := c.GetString(gin.AuthUserKey) // used in sync.Map usedfiles.
	if username == "" {
		// anonymous users are not allowed to upload to a full path of the file.
		fullpath = name
	}
	strsha1 := c.GetHeader("Sha1") // unnecessary file checksum.
	return userquery{
		fullpath:        fullpath,
		storagepath:     storagepath,
		name:            name,
		username:        username,
		strsha1:         strsha1,
		nameNotComplete: name + ".part",
	}, nil
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
	// WAITS
	retwriteresult = <-chWriteResult

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

// GetPathWhereToStoreByUsername returns a user storage path.
func GetPathWhereToStoreByUsername(username string) string {
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
func closeFiles(wa, wp *os.File) []error {
	err1 := wa.Close()
	err2 := wp.Close()
	if err1 != nil || err2 != nil {
		slerrors := make([]error, 0, 2)
		slerrors = append(slerrors, err1)
		slerrors = append(slerrors, err2)
		return slerrors
	}
	return nil
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
		log.Println(logline(c, fmt.Sprintf("OK SHA1 %x, file %s", factsha1, name)))

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
