package main

// substitution is
// curl.exe -v -X POST 'http://127.0.0.1:64000/upload/zahar?&Filename="sendfile.rar"' -T .\testbackups\sendfile.rar --anyauth --user zahar
import (
	Error "Upload/errstr"
	"Upload/fsdriver"
	"Upload/httpDigestAuthentication"
	"Upload/liteimp"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh/terminal"
	"strings"

	//"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	//"hash"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

var (
// errors equality by Code(or Descr), not by value, because values may be return from goroutines simultaneously.
//errServerRespondedWithBadJson = Error.E("uploader", nil, Error.ErrEncodingDecoding,"Server responded with bad json structure.")
//errStatusContinueExpected     = *errstr.NewError("uploader", 2, "We expect status 100-Continue.")
//errServerDidNotAdmitUpload    = *errstr.NewError("uploader", 8, "Server did not admit upload. We can't be sure of successfull upload.")

)

var where someconfig

type someconfig struct {
	toURL    string
	password string // one need to translate []byte to propper utf-8 string
	username string
}

func redirectPolicyFunc(_ *http.Request, _ []*http.Request) error {

	return nil
}

// sendAFile sends file to a service Upload.
// jar holds cookies from server http.Responses and use them in http.Requests
func sendAFile(where *someconfig, fullfilename string, jar *cookiejar.Jar, bsha1 []byte) error {
	// I use op as the first argument to Error.E()
	const op = "uploader.sendAFile()"

	// opens the file
	_, name := filepath.Split(fullfilename)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {
		return Error.E(op, err, errCantOpenFileForReading, 0, "")
	}
	// closes file on exit
	defer func() { _ = f.Close() }()

	// reads file size
	stat, err := f.Stat()
	if err != nil {
		return Error.E(op, err, errCantGetFileProperties, 0, "")
	}

	// creates http.Request
	req, err := http.NewRequest("POST", where.toURL, f)
	// f will be closed after http.Client.Do
	if err != nil {
		return Error.E(op, err, errCantCreateHTTPRequest, 0, "")
	}
	// use context to define timeout of total http.Request
	// ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	// req = req.WithContext(ctx)

	req.ContentLength = stat.Size()          // file size
	req.Header.Add("Expect", "100-continue") // client will not send body at once, it will wait for server response status "100-continue"
	req.Header.Add("sha1", fmt.Sprintf("%x", bsha1))

	query := req.URL.Query()
	query.Add("filename", name) // url parameter &filename
	req.URL.RawQuery = query.Encode()

	// I use transport to define timeouts: idle and expect timeout
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 20 * time.Second, // wait for headers for how long
		TLSHandshakeTimeout:   20 * time.Second, // time to negotiate for TLS
		IdleConnTimeout:       5 * time.Minute,  // server responded but connection is idle for how long
		ExpectContinueTimeout: 20 * time.Second, // expects response status 100-continue before sending the request body
	}

	// use http.Client to define cookies jar and transport usage
	cli := &http.Client{
		CheckRedirect: redirectPolicyFunc,
		Timeout:       5 * time.Minute, // we connected to ip port but didn't manage to read the whole response (headers and body) within Timeout
		Transport:     tr,              // I don't use http.DefaultTransport
		Jar:           jar,             // http.Request uses jar to keep cookies (to hold sessionID)
	}

	waitBeforeRetry := time.Duration(30) * time.Second

	ret := Error.E(op, err, errNumberOfRetriesExceeded, 0, "")
	authorizationsent := false

	// cycle for some errors that can be tolerated
	for i := 0; i < 3; i++ {
		// every step makes a request with a new body bytes (continues upload)
		req.Body = f
		// the first request is without a cookie. Other requests comes with sessionID in cookie.
		resp, err := cli.Do(req)       // req sends a file f in the body. cli.Do _closes_ file f.
		if err != nil || resp == nil { // response may be nil when transport fails with timeout (it may timeout while i am debugging the upload server)
			ret = Error.E(op, err, errCantConnectToServer, 0, "")
			log.Printf("%s", ret)

			time.Sleep(waitBeforeRetry) // waits

			// opens file again
			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
				log.Printf("%s", ret)
				return ret
			}

			continue // retries
		}

		log.Printf("Connected to %s", req.URL)
		if resp.StatusCode == http.StatusAccepted {
			_ = resp.Body.Close()

			return nil // upload completed successfully
		}
		if resp.StatusCode == http.StatusForbidden {
			_ = resp.Body.Close()
			// server actively denies upload
			return Error.E(op, nil, errServerForbiddesUpload, Error.ErrKindInfoForUsers, "")
		}
		if resp.StatusCode == http.StatusUnauthorized && authorizationsent {
			log.Printf("Username or password is incorrect.")
			return Error.E(op, nil, errAuthorizationFailed, 0, "")
		}
		if resp.StatusCode == http.StatusUnauthorized {
			wwwauthstr := resp.Header.Get("WWW-Authenticate")
			_ = resp.Body.Close()
			if wwwauthstr == "" || !strings.HasPrefix(wwwauthstr, "Digest") {
				return Error.E(op, nil, errBadHTTPAuthanticationMethod, 0, "")

			}
			challengeAndCredentials, err := httpDigestAuthentication.ParseStringIntoStruct(wwwauthstr)
			if err != nil {
				return Error.E(op, err, errBadHTTPAuthenticationChellenge, 0, "")
			}
			// we support Digest http authentication only

			// check for supported by us digest mode
			if challengeAndCredentials.Algorithm != "MD5" {
				return Error.E(op, nil, errBadHTTPAuthenticationChellenge, 0, "")
			}
			if challengeAndCredentials.Qop != "auth" {
				return Error.E(op, nil, errBadHTTPAuthenticationChellenge, 0, "")

			}
			// client must supply its cnonce value
			challengeAndCredentials.Cnonce = uuid.New().String()
			// don't forget to fill challengeAndCredentials.Method
			challengeAndCredentials.Method = req.Method

			hashUsernameRealmPassword := httpDigestAuthentication.HashUsernameRealmPassword(where.username, challengeAndCredentials.Realm, where.password)
			responseParam, err := httpDigestAuthentication.GenerateResponseAuthorizationParameter(hashUsernameRealmPassword, challengeAndCredentials)
			challengeAndCredentials.Username = where.username
			challengeAndCredentials.Response = responseParam
			req.Header.Set("Authorization", httpDigestAuthentication.GenerateAuthorization(challengeAndCredentials))
			authorizationsent = true
			continue

		}

		// next we read resp.Body and need to close it.

		// Open file again as we are going to do the next upload attempt.
		// The rest IFs are for expected "error" http.StatusConflist and other upload service specific errors.
		// Such as service messed something and asks us to retry.
		f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
		if err != nil {
			_ = resp.Body.Close()
			ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
			log.Printf("%s", ret)
			return ret
		}

		if resp.StatusCode == http.StatusConflict { // we expect StatusConflict, it means we are to continue upload.
			// server responded "a file already exists" with JSON
			filestatus, debugbytes, err := decodeJSONinBody(resp)

			_ = resp.Body.Close() // don't need resp.Body any more

			if err != nil {
				// logs incorrect server respone

				ret = Error.E(op, err, errServerRespondedWithBadJSON, 0, string(debugbytes))
				log.Printf("%s", ret)
				return ret // do not retry, just return
			}
			// server sent a proper json response

			startfrom := filestatus.Startoffset
			const fromBegin = 0 // startfrom bytes from the beginning
			newoffset, err := f.Seek(startfrom, fromBegin)

			if err != nil || newoffset != startfrom {
				ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
				log.Printf("%s", ret)
				return ret // do not retry, just return
			}

			bytesleft := stat.Size() - newoffset

			query = req.URL.Query()
			query.Set("startoffset", strconv.FormatInt(newoffset, 10))
			query.Set("count", strconv.FormatInt(bytesleft, 10))
			query.Set("filename", name)
			req.URL.RawQuery = query.Encode()
			log.Printf("continue from startoffset %d", newoffset)
			// no delay, do expected request again
			continue // cycles to next cli.Do()

		}

		{
			log.Printf("DEBUG:Server responded with status: %s", resp.Status)
			b := make([]byte, 500)
			n, _ := resp.Body.Read(b)
			b = b[:n]
			log.Printf("DEBUG:%s", string(b))
		}
		// here goes other errors and http.statuses:
		// upload failed for some reson, timeouts. Retry with current cookie.

		time.Sleep(waitBeforeRetry)
	}
	return ret

}

// decodeJSONinBody tries to decode resp.Body as a JSON of type liteimp.JsonFileStatus.
// Doesn't close the resp.Body.
func decodeJSONinBody(resp *http.Response) (value *liteimp.JsonFileStatus, debugbytes []byte, err error) {

	const op = "uploader.decodeJSONinBody()"

	value = &liteimp.JsonFileStatus{} // struct
	if resp.ContentLength == 0 {
		ret := Error.E(op, err, errServerRespondedWithBadJSON, 0, "")
		return value, nil, ret
	}
	b := make([]byte, resp.ContentLength) // expects that server has responded with some json
	maxjsonlen := int64(4000)
	ioreader := io.LimitReader(resp.Body, maxjsonlen)

	_, err = ioreader.Read(b)

	if !(err == nil || err == io.EOF) {
		ret := Error.E(op, err, errServerRespondedWithBadJSON, 0, "")
		return value, nil, ret
	}
	// unmarshal server json response
	if err := json.Unmarshal(b, &value); err != nil {
		ret := Error.E(op, err, errServerRespondedWithBadJSON, 0, "")
		return value, b, ret
	}
	return value, nil, nil

}

func main() {
	const op = "main()"
	logname := flag.String("log", "", "log file.")
	file := flag.String("file", "", "a file you want to upload.")
	dirtomonitor := flag.String("dir", "", "a directory which to upload.")
	username := flag.String("username", "", "user name of an Upload service.")
	uploadServerURL := flag.String("service", `http://127.0.0.1:64000/upload`, "URL of the Upload service.")
	askpassword := flag.Bool("askpassword", true, "will ask your password for the Upload service.")
	//passwordfile := flag.Srtring("passwordfile","","a file with password (Windows DPAPI encrypted).")

	flag.Parse()
	if len(os.Args[1:]) == 0 {
		flag.PrintDefaults()
		os.Exit(1)
		return
	}
	// check required parameters
	where.toURL = *uploadServerURL

	if *file == "" && *dirtomonitor == "" {
		log.Printf("--file or --dir must be specified.")
		os.Exit(1)
		return
	}
	// asks password only if there is a specified user
	if *askpassword && *username != "" {
		fmt.Printf("\nEnter user '%s' password: ", *username)
		password, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		println("")
		if err != nil {
			fmt.Printf("Reading password error: %s", err)
			return
		}
		where.password = string(password)
	}
	if *username != "" {
		where.username = *username
		if where.toURL[len(where.toURL)-1] != '/' {
			where.toURL += "/"
		}
		where.toURL += *username
	}

	defer func() {
		// on panic we will write to log file
		if err := recover(); err != nil {

			log.Printf("uploader main has paniced:\n%s\n", err)
			b := make([]byte, 2500) // enough buffer
			n := runtime.Stack(b, true)
			b = b[:n]
			// logs stack trace
			log.Printf("%d bytes of stack trace.\n%s", n, string(b))
		}
	}()
	// setup log destination
	var flog *os.File
	if *logname == "" {
		flog = os.Stdout
	} else {
		var err error
		flog, err = os.OpenFile(*logname, os.O_APPEND|os.O_CREATE, os.ModeAppend)
		log.Printf("%s", Error.E(op, err, errCantOpenFileForReading, 0, ""))
		os.Exit(1)
		return
	}

	// from hereafter use log for messages
	log.SetOutput(flog)
	defer func() { _ = flog.Close() }()

	// channel with filenames
	chNames := make(chan string, 2)

	if *file != "" {
		// send to chNames
		chNames <- *file
	}
	if *dirtomonitor != "" {
		// walk a dir, send names to chNames
		go getFilenamesToupload(*dirtomonitor, chNames) // closes chNames after adding all files
	} else {
		close(chNames)
	}
	// TODO(zavla): import "github.com/fsnotify/fsnotify"
	// TODO(zavla): store partial files aside? move them somewhere?
	// TODO(zavla): run uploadserver as a Windows service.
	// TODO(zavla): autotls?
	// TODO(zavla): CSRF, do not mix POST request with URL parametres!
	// TODO(zavla): in server calculate speed of upload.

	runWorkers(chNames)
	log.Println("Normal exit.")
}

// runWorkers starts goroutines
func runWorkers(ch chan string) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go worker(&wg, ch)
	}
	wg.Wait()

}

// worker takes from channel and sends files
func worker(wg *sync.WaitGroup, ch chan string) {
	defer wg.Done()

	for name := range ch {
		prepareAndSendAFile(name, &where)
	}
	return
}

func prepareAndSendAFile(filename string, config *someconfig) {
	const op = "uploader.prepareAndSendAFile()"
	// uses cookies to hold sessionId
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // never error

	fullfilename := filepath.Clean(filename)
	storagepath := filepath.Dir(fullfilename)
	name := filepath.Base(fullfilename)

	//compute SHA1 of a file
	bsha1, err := fsdriver.GetFileSha1(storagepath, name)
	if err != nil {
		log.Printf("%s", Error.E(op, err, errCantOpenFileForReading, 0, ""))
		return
	}

	err = sendAFile(config, fullfilename, jar, bsha1)

	if err == nil {
		log.Printf("Upload succsessful: %s", fullfilename)

		if err := markFileAsUploaded(fullfilename); err != nil {
			// a non critical error
			log.Printf("%s", Error.E(op, err, errMarkFileFailed, 0, ""))
		}
		// SUCCESS
		return
	}

	log.Printf("%s", err)
	return
}

// getFilenamesToupload collects files names in a directory and sends them to channel chNames.
func getFilenamesToupload(dir string, chNames chan<- string) {
	const op = "uploader.getFilenamesToupload()"
	defer close(chNames)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil // next file please
		}
		// uses "archive" attribute on Windows and FS_NODUMP_FL file attribute on linux.
		isarchiveset, _ := getArchiveAttribute(path)
		if isarchiveset {
			chNames <- path
		}
		return nil // next file please
	})
	if err != nil {
		close(chNames)
		log.Printf("%s", Error.E(op, err, errReadingDirectory, 0, ""))
	}

	return
}
