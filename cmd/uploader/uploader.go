package main

import (
	"Upload/errstr"
	"Upload/liteimp"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
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
	"golang.org/x/sys/windows"
)

var (
	errServerRespondedWithBadJson = *errstr.NewError("uploader", 1, "Server responded with bad json structure.")
	errStatusContinueExpected     = *errstr.NewError("uploader", 2, "We expect status 100-Continue.")
	errCantOpenFileForReading     = *errstr.NewError("uploader", 3, "Can't open file for reading.")
	errCantGetFileProperties      = *errstr.NewError("uploader", 4, "Can't get file properties.")
	errCantCreateHttpRequest      = *errstr.NewError("uploader", 5, "Can't create http.Request")
	errCantConnectToServer        = *errstr.NewError("uploader", 6, "Can't connect to http server")
	errFileSeekErrorOffset        = *errstr.NewError("uploader", 7, "File seek offset error.")
	errServerDidNotAdmitUpload    = *errstr.NewError("uploader", 8, "Server did not admit upload. We can't be sure of successfull upload.")
	errNumberOfRetriesExceeded    = *errstr.NewError("uploader", 9, "Number of retries exceeded.")
	errServerForbiddesUpload      = *errstr.NewError("", 10, "Server fobidded upload. File already exists.")
)

// jar holds cookies and used by http.Client to get cookies from Response and to set cookies into Request
var jar cookiejar.Jar

//const uploadServerURL = `http://127.0.0.1:64000/upload?&Filename=sendfile.rar`
const uploadServerURL = `http://127.0.0.1:64000/upload`

//const uploadServerURL = `http://myapp/upload`

// eoe = "exit on error"
// args are pairs of key,value. Even number of args expected.
func eoe(exit bool, err error, descr string, args ...interface{}) {
	if err != nil {
		var logline bytes.Buffer
		logline.WriteString(fmt.Sprintf("%s\n%s\n", descr, err))
		count := len(args)
		for i := 0; i < count/2; i++ {
			logline.WriteString(fmt.Sprintf("%s=%s; ", args[i], args[i+1]))
		}
		log.Println(logline)
		if exit {
			log.Fatal()
		} //exits
	}
	return
}

// SendAFile sends file to a micro service.
// jar holds cookies from server http.Responses and use them in http.Requests
func SendAFile(addr string, fullfilename string, jar *cookiejar.Jar, hsha1 hash.Hash) error {
	// opens file
	_, name := filepath.Split(fullfilename)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {
		return errCantOpenFileForReading.SetDetails("filename=%s", fullfilename)
	}
	// closes file on exit
	defer f.Close()

	// reads file size
	stat, err := f.Stat()
	if err != nil {
		return errCantGetFileProperties.SetDetails("filename=%s", fullfilename)
	}

	// creates http.Request
	req, err := http.NewRequest("POST", uploadServerURL, f) // reads body from f, f will be close after http.Client.Do
	if err != nil {
		return errCantCreateHttpRequest
	}
	// use context to define timeout of total http.Request
	// ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	// req = req.WithContext(ctx)

	req.ContentLength = stat.Size()          // file size
	req.Header.Add("Expect", "100-continue") // client will not send body at once, it will wait for server response status "100-continue"
	req.Header.Add("sha1", fmt.Sprintf("%x", hsha1.Sum(nil)))

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

	waitBeforRetrySec := time.Duration(10)

	ret := errNumberOfRetriesExceeded // func return value
	// cycle for some errors that can be tolarated
	for i := 0; i < 3; i++ {

		req.Body = f
		// makes the first request, without cookie or makes a retry request with sessionID in cookie on steps 2,3...
		resp, err := cli.Do(req)       // sends a file f in the body,  closes file f
		if err != nil || resp == nil { // response may be nil when transport fails with timeout (timeout while i am debugging the upload server)
			ret = *errCantConnectToServer.SetDetailsSubErr(err, "server %s", req.URL)
			log.Printf("%s", ret)
			time.Sleep(waitBeforRetrySec * time.Second) // waits

			// opens file again
			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				ret = *errCantOpenFileForReading.SetDetailsSubErr(err, "file %s", fullfilename)
				log.Printf("%s", ret)
				return ret
			}

			continue // retries
		}

		resp.Body.Close()

		log.Printf("Connected to %s", req.URL)
		if resp.StatusCode == http.StatusAccepted {
			resp.Body.Close()

			return nil // upload completed
		}

		// opens file again
		f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
		if err != nil {
			ret = *errCantOpenFileForReading.SetDetailsSubErr(err, "Can't open the file: %s", fullfilename)
			log.Printf("%s", ret)
			return ret
		}

		if resp.StatusCode == http.StatusConflict { // we expect StatusConflict
			var bodyjson liteimp.JsonFileStatus
			// server responded "a file already exists"
			fromserverjson, err, debugbytes := UnmarshalBody(resp) // unmarshals to JsonFileStatus,closes body
			// response Body (resp.Body) here is closed
			if err != nil {
				// logs incorrect server respone
				msgbytes, _ := json.MarshalIndent(bodyjson, "", " ")
				ret = *errServerRespondedWithBadJson.SetDetailsSubErr(err, "want = %s", string(msgbytes))
				log.Printf("Got = %s\n%s", string(debugbytes), ret)
				return ret // do not retry, just return
			}
			// server sended a proper json response

			startfrom := fromserverjson.Startoffset
			newoffset, err := f.Seek(startfrom, 0) // 0 = seek from the begining
			if err != nil || newoffset != startfrom {
				ret = *errFileSeekErrorOffset.SetDetailsSubErr(err, "server wants offset=%d , we can only offset to=%d", startfrom, newoffset)
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
			return errServerForbiddesUpload
		} else {
			log.Printf("Server responded with error, status: %s", resp.Status)
			b := make([]byte, 500)
			n, _ := resp.Body.Read(b)
			b = b[:n]
			log.Printf("%s", string(b))
		}

		time.Sleep(waitBeforRetrySec * time.Second)
		// upload failed or timed out? retry with current cookie
	}
	return ret

}

func UnmarshalBody(resp *http.Response) (value *liteimp.JsonFileStatus, err error, debugbytes []byte) {
	value = &liteimp.JsonFileStatus{} // struct
	if resp.ContentLength == 0 {
		return value, errServerRespondedWithBadJson, nil
	}
	b := make([]byte, resp.ContentLength) // expects that server has responded with some json
	maxjsonlen := int64(4000)
	ioreader := io.LimitReader(resp.Body, maxjsonlen)

	_, err = ioreader.Read(b)
	defer resp.Body.Close()
	if !(err == nil || err == io.EOF) {
		return value, errServerRespondedWithBadJson, nil
	}
	// unmarshal server json response
	if err := json.Unmarshal(b, &value); err != nil {
		return value, errServerRespondedWithBadJson, b
	}
	return value, nil, nil

}

func main() {
	logname := flag.String("log", "", "log file path and name.")
	file := flag.String("file", "", "a file you want to upload")
	dirtomonitor := flag.String("dir", "", "a directory which to monitor for new files to upload")
	flag.Parse()
	if len(os.Args) == 0 {
		flag.PrintDefaults()
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
		eoe(true, err, "Can't start without log file", "file", *logname) // do not start without log file

	}

	// from hereafter use log for messages
	log.SetOutput(flog)

	// walk a dir

	nameslist := make([]string, 0, 200)
	if *dirtomonitor != "" {
		nameslist = GetFilenamesThatNeedUpload(*dirtomonitor, nameslist)
	}
	if *file != "" {
		nameslist = append(nameslist, *file)
	}
	// TODO(zavla): import "github.com/fsnotify/fsnotify"
	// TODO(zavla): talkative errors?
	// TODO(zavla): store partial files aside?
	// TODO(zavla): run uploadserver as a Windows service.
	// TODO(zavla): everyfunc to lowcase
	// channel with filenames
	chNames := make(chan string, 2)

	go puttoqueue(chNames, nameslist)
	runWorkers(chNames)
	log.Println("uploader exited.")
}
func puttoqueue(ch chan string, sl []string) {
	for _, name := range sl {

		ch <- name
	}
	close(ch) // range loop will read all elements from buffered channel if they are there
	return
}
func runWorkers(ch chan string) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go worker(&wg, ch)
	}
	wg.Wait()

}
func worker(wg *sync.WaitGroup, ch chan string) {
	defer wg.Done()

	for name := range ch {
		PrepareAndSendAFile(name)
	}
	return
}
func PrepareAndSendAFile(filename string) {
	// uses cookies to hold sessionId
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // never error

	fullfilename := filepath.Clean(filename)

	//compute SHA1 of a file
	hsha1, err := GetFileSHA1(fullfilename)
	if err != nil {
		log.Printf("Can't read file. %s", err)

		return
	}

	err = SendAFile(uploadServerURL, fullfilename, jar, hsha1)

	if err == nil {
		log.Printf("upload succsessful: file %s", fullfilename)
		if err := MarkFileAsUploaded(fullfilename); err != nil {
			// a non critical error
			log.Printf("Can's set file attribute: %s", err)
		}
		return
	}
	if err == errServerForbiddesUpload {
		log.Printf("Server do not allow this file upload: %s", fullfilename)
		return
	}
	return
}

func MarkFileAsUploaded(fullfilename string) error {
	// uses Windows API
	ptrFilenameUint16, err := windows.UTF16PtrFromString(fullfilename)
	if err != nil {
		log.Printf("Can't convert filename to UTF16 %s", fullfilename)
		return err
	}
	attr, err := windows.GetFileAttributes(ptrFilenameUint16)
	if err != nil {
		log.Printf("Can't get file attributes: %s", err)
		return err
	}
	if attr&windows.FILE_ATTRIBUTE_ARCHIVE != 0 {
		err := windows.SetFileAttributes(ptrFilenameUint16, attr^windows.FILE_ATTRIBUTE_ARCHIVE)
		if err != nil {
			log.Printf("Can't set file archive attribute to 0: %s", err)
			return err
		}
	}
	return nil

}

func GetArchiveAttribute(fullfilename string) bool {
	ptrFilename, err := windows.UTF16PtrFromString(fullfilename)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(ptrFilename)
	if err != nil {
		return false
	}
	return (attrs & windows.FILE_ATTRIBUTE_ARCHIVE) != 0
}
func GetFileSHA1(fullfilename string) (hash.Hash, error) {
	//f, err := os.OpenFile(fullfilename, os.O_RDONLY|os.O_EXCL, 0)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {

		return nil, err
	}
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h, nil
}

func GetFilenamesThatNeedUpload(dir string, nameslist []string) []string {

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil // next file please
		}
		// TODO(zavla): decide how to store "archive" attribute in linux
		isarchiveset := GetArchiveAttribute(path)
		if isarchiveset {
			nameslist = append(nameslist, path)
		}
		return nil
	})
	return nameslist
}
