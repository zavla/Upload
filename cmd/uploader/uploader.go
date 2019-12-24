package main

import (
	Error "Upload/errstr"
	"Upload/fsdriver"
	"Upload/liteimp"
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

//const uploadServerURL = `http://127.0.0.1:64000/upload?&Filename=sendfile.rar`
var uploadServerURL = `http://127.0.0.1:64000/upload`

//const uploadServerURL = `http://myapp/upload`

// sendAFile sends file to a service Upload.
// jar holds cookies from server http.Responses and use them in http.Requests
func sendAFile(toURL string, fullfilename string, jar *cookiejar.Jar, bsha1 []byte) error {
	// I use op as the first argument to Error.E()
	const op = "uploader.sendAFile()"
	// opens file
	_, name := filepath.Split(fullfilename)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {
		return Error.E(op, err, ErrCantOpenFileForReading, 0, "")
	}
	// closes file on exit
	defer func() { _ = f.Close() }()

	// reads file size
	stat, err := f.Stat()
	if err != nil {
		return Error.E(op, err, ErrCantGetFileProperties, 0, "")
	}

	// creates http.Request
	req, err := http.NewRequest("POST", toURL, f) // reads body from f, f will be close after http.Client.Do
	if err != nil {
		return Error.E(op, err, ErrCantCreateHttpRequest, 0, "")
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

	// use transport to define timeouts: idle and expect timeout
	tr := &http.Transport{

		ResponseHeaderTimeout: 20 * time.Second, // wait for headers for how long
		TLSHandshakeTimeout:   20 * time.Second, // time to negotiate for TLS
		IdleConnTimeout:       5 * time.Minute,  // server responded but connection is idle for how long
		ExpectContinueTimeout: 20 * time.Second, // expects response status 100-continue befor sending body
	}

	// use http.Client to define cookies jar and transport usage
	cli := &http.Client{
		Timeout:   5 * time.Minute, // we connected to ip port but didn't manage to read the whole respone (headers and body) within Timeout
		Transport: tr,
		Jar:       jar, // http.Request uses jar to keep cookies (to hold sessionID)
	}

	waitBeforeRetry := time.Duration(10)

	ret := Error.E(op, err, ErrNumberOfRetriesExceeded, 0, "")
	// cycle for some errors that can be tolerated
	for i := 0; i < 3; i++ {

		req.Body = f
		// makes the first request, without cookie or makes a retry request with sessionID in cookie on steps 2,3...
		resp, err := cli.Do(req)       // sends a file f in the body,  closes file f
		if err != nil || resp == nil { // response may be nil when transport fails with timeout (timeout while i am debugging the upload server)
			ret = Error.E(op, err, ErrCantConnectToServer, 0, "")
			log.Printf("%s", ret)
			time.Sleep(waitBeforeRetry * time.Second) // waits

			// opens file again
			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				ret = Error.E(op, err, ErrCantOpenFileForReading, 0, "")
				log.Printf("%s", ret)
				return ret
			}

			continue // retries
		}

		// TODO(zavla): when to close body ???
		//???resp.Body.Close()

		log.Printf("Connected to %s", req.URL)
		if resp.StatusCode == http.StatusAccepted {
			_ = resp.Body.Close()

			return nil // upload completed
		}

		// opens file again
		f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
		if err != nil {
			ret = Error.E(op, err, ErrCantOpenFileForReading, 0, "")
			log.Printf("%s", ret)
			return ret
		}

		if resp.StatusCode == http.StatusConflict { // we expect StatusConflict
			//var bodyjson liteimp.JsonFileStatus
			// server responded "a file already exists"
			fromserverjson, err, debugbytes := decodeJSONinBody(resp) // unmarshals to JsonFileStatus,closes body
			// response Body (resp.Body) here is closed
			if err != nil {
				// logs incorrect server respone
				//msgbytes, _ := json.MarshalIndent(bodyjson, "", " ")
				ret = Error.E(op, err, ErrServerRespondedWithBadJson, 0, "")
				log.Printf("Got = %s\n%s", string(debugbytes), ret)
				return ret // do not retry, just return
			}
			// server sended a proper json response

			startfrom := fromserverjson.Startoffset
			newoffset, err := f.Seek(startfrom, 0) // 0 = seek from the begining
			if err != nil || newoffset != startfrom {
				ret = Error.E(op, err, ErrFileSeekErrorOffset, 0, "")
				log.Printf("%s", ret)
				return ret // do not retry, just return
			}

			bytesleft := stat.Size() - newoffset

			query = req.URL.Query()
			query.Set("startoffset", strconv.FormatInt(newoffset, 10))
			query.Set("count", strconv.FormatInt(bytesleft, 10))
			query.Set("filename", name)
			req.URL.RawQuery = query.Encode()
			log.Printf("second request with startoffset %s", req.URL.RawQuery)
			// no delay, do expected request again
			continue // cycles to next retry

		} else if resp.StatusCode == http.StatusForbidden {
			return Error.E(op, nil, ErrServerForbiddesUpload, Error.ErrKindInfoForUsers, "")
		} else {
			log.Printf("Server responded with error, status: %s", resp.Status)
			b := make([]byte, 500)
			n, _ := resp.Body.Read(b)
			b = b[:n]
			log.Printf("%s", string(b))
		}

		time.Sleep(waitBeforeRetry * time.Second)
		// upload failed or timed out? retry with current cookie
	}
	return ret

}

func decodeJSONinBody(resp *http.Response) (value *liteimp.JsonFileStatus, err error, debugbytes []byte) {

	const op = "uploader.decodeJSONinBody()"

	value = &liteimp.JsonFileStatus{} // struct
	if resp.ContentLength == 0 {
		ret := Error.E(op, err, ErrServerRespondedWithBadJson, 0, "")
		return value, ret, nil
	}
	b := make([]byte, resp.ContentLength) // expects that server has responded with some json
	maxjsonlen := int64(4000)
	ioreader := io.LimitReader(resp.Body, maxjsonlen)

	_, err = ioreader.Read(b)
	defer func() { _ = resp.Body.Close() }()
	if !(err == nil || err == io.EOF) {
		ret := Error.E(op, err, ErrServerRespondedWithBadJson, 0, "")
		return value, ret, nil
	}
	// unmarshal server json response
	if err := json.Unmarshal(b, &value); err != nil {
		ret := Error.E(op, err, ErrServerRespondedWithBadJson, 0, "")
		return value, ret, b
	}
	return value, nil, nil

}

func main() {
	const op = "main()"
	logname := flag.String("log", "", "log file path and name.")
	file := flag.String("file", "", "a file you want to upload")
	dirtomonitor := flag.String("dir", "", "a directory which to monitor for new files to upload")
	flag.StringVar(&uploadServerURL, "server", `http://127.0.0.1:64000/upload`, "URL of upload server")
	flag.Parse()
	if len(os.Args) == 0 {
		flag.PrintDefaults()
		os.Exit(1)
		return
	}

	defer func() {
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
		log.Printf("%s", Error.E(op, err, ErrCantOpenFileForReading, 0, ""))
		os.Exit(1)
		return
	}

	// from hereafter use log for messages
	log.SetOutput(flog)
	defer func() { _ = flog.Close() }()

	// check required parameters
	if *file == "" && *dirtomonitor == "" {
		log.Printf("--file or --dir must be specified.")
		os.Exit(1)
		return
	}

	// channel with filenames
	chNames := make(chan string, 2)

	if *file != "" {
		chNames <- *file
	}
	if *dirtomonitor != "" {
		// walk a dir
		go getFilenamesToupload(*dirtomonitor, chNames) // closes chNames after adding all files
	} else {
		close(chNames)
	}
	// TODO(zavla): import "github.com/fsnotify/fsnotify"
	// TODO(zavla): make errors more understandable
	// TODO(zavla): store partial files aside? move them somewhere?
	// TODO(zavla): run uploadserver as a Windows service.
	// TODO(zavla): every in fact nonexported func make lowcase
	// TODO(zavla): autotls?
	// TODO(zavla): CSRF, do not mix POST request with URL parametres!
	// TODO(zavla): in server calculate speed of of upload.
	// TODO(zavla): get rid of Sync() in fsdriver.AddBytesToFile() ?

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
		prepareAndSendAFile(name)
	}
	return
}

func prepareAndSendAFile(filename string) {
	const op = "uploader.prepareAndSendAFile()"
	// uses cookies to hold sessionId
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // never error

	fullfilename := filepath.Clean(filename)
	storagepath := filepath.Dir(fullfilename)
	name := filepath.Base(fullfilename)

	//compute SHA1 of a file
	bsha1, err := fsdriver.GetFileSha1(storagepath, name)
	if err != nil {
		log.Printf("%s", Error.E(op, err, ErrCantOpenFileForReading, 0, ""))
		return
	}

	err = sendAFile(uploadServerURL, fullfilename, jar, bsha1)

	if err == nil {
		log.Printf("Upload succsessful: %s", fullfilename)

		if err := MarkFileAsUploaded(fullfilename); err != nil {
			// a non critical error
			log.Printf("%s", Error.E(op, err, ErrMarkFileFailed, 0, ""))
		}
		// SUCCESS
		return
	}

	log.Printf("%s", err)
	return
}

// getFilenamesToupload collects files names to channel chNames <-
func getFilenamesToupload(dir string, chNames chan<- string) {
	const op = "uploader.getFilenamesToupload()"
	defer close(chNames)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil // next file please
		}
		// uses "archive" attribute on Windows and FS_NODUMP_FL file attribute on linux.
		isarchiveset, _ := GetArchiveAttribute(path)
		if isarchiveset {
			chNames <- path
		}
		return nil // next file please
	})
	if err != nil {
		close(chNames)
		log.Printf("%s", Error.E(op, err, ErrReadingDirectory, 0, ""))
	}

	return
}
