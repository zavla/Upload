package main

import (
	"Upload/errstr"
	"Upload/liteimp"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/net/publicsuffix"
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
)

// jar holds cookies and used by http.Client to get cookies from Response and to set cookies into Request
var jar cookiejar.Jar

//const bindAddress = `http://127.0.0.1:64000/upload?&Filename=sendfile.rar`
const bindAddress = `http://127.0.0.1:64000/upload`

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
func SendAFile(addr string, fullfilename string, jar *cookiejar.Jar) error {
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

	// fills http request params

	req, err := http.NewRequest("POST", bindAddress, f) // reads body from f, f will be close after http.Client.Do
	if err != nil {
		return errCantCreateHttpRequest
	}
	// use context to define timeout of total http.Request
	//ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	//req = req.WithContext(ctx)

	req.ContentLength = stat.Size()          // file size
	req.Header.Add("Expect", "100-continue") // client will not send body at once, will wait for server 100-continue

	query := req.URL.Query()
	query.Add("filename", name) // url parameter &filename
	req.URL.RawQuery = query.Encode()

	// use transport
	tr := &http.Transport{
		IdleConnTimeout:       5 * time.Minute,  // server responded but connection is idle
		ExpectContinueTimeout: 20 * time.Second, // expects response status 100-continue befor sending body
	}
	cli := &http.Client{
		Timeout:   5 * time.Minute, // we connected to ip port but server didnot respond within Timeout
		Transport: tr,
		Jar:       jar, // http.Request uses jar to keep cookies (to hold sessionID)
	}
	var ret error
	for i := 0; i < 5; i++ {
		// makes the first request, without cookie or makes a retry request with sessionID in cookie
		resp, err := cli.Do(req) // sends a file f in the body,  closes f
		if err != nil {
			log.Printf("Can't connect to server: %s", err)
			ret = errCantConnectToServer.SetDetails("Address=%s", req.URL)
		}
		log.Printf("Connected to: %s", req.URL)
		if resp == nil {
			log.Printf("resp==nil")
		}
		if resp.StatusCode == http.StatusConflict { // we expect StatusConflict
			log.Printf("in the if resp.StatusCode == http.StatusConflict { // we expect StatusConflict\n")
			var bodyjson liteimp.JsonFileStatus
			// server responded "a file already exists"
			fromserverjson, err, debugbytes := UnmarshalBody(resp) // unmarshals to JsonFileStatus,closes body
			if err != nil {
				msgbytes, _ := json.MarshalIndent(bodyjson, "", " ")
				log.Printf("Got = %s\n%s", string(debugbytes), errServerRespondedWithBadJson.SetDetails("want = %s", string(msgbytes)))
				return errServerRespondedWithBadJson
			}
			// server sended a proper json response
			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				terr := errCantOpenFileForReading.SetDetailsSubErr(err, "Cant's open file")
				log.Printf("%s", *terr)
				return terr
			}

			startfrom := fromserverjson.Startoffset
			newoffset, err := f.Seek(startfrom, 0) // 0 = seek from the begining
			if err != nil || newoffset != startfrom {
				terr := errFileSeekErrorOffset.SetDetailsSubErr(err, "server wants offset=%d , we can only offset to=%d", startfrom, newoffset)
				log.Printf("%s", *terr)
				return *terr
			}

			bytesleft := stat.Size() - newoffset

			query = req.URL.Query()
			query.Set("startoffset", string(newoffset))
			query.Set("count", string(bytesleft))
			query.Set("filename", name)
			req.URL.RawQuery = query.Encode()
			req.Body = f
			continue
			// next retry

		}
		if resp.StatusCode == http.StatusAccepted {
			return nil
		}
		time.Sleep(2 * time.Second)
		// upload failed or timed out? retry with current cookie
	}
	return ret

}

// RequestWithBody do a http.Request on http.Client, adds jsoned value to bodybytes.
func RequestWithBody(cli *http.Client, req *http.Request, value liteimp.JsonFileStatus, bodybytes *bytes.Buffer) (resp *http.Response, err error) {
	b, err := json.Marshal(value)
	bodybytes.Write(b)
	req.Body = ioutil.NopCloser(bodybytes)
	resp, err = cli.Do(req)
	return //named
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
	flag.Parse()
	count := 3
	defer func() {
		if err := recover(); err != nil {
			count = count + 1 // on panic increment
			log.Printf("uploader main has paniced:\n%s\n", err)
			b := make([]byte, 2500) // enough buffer
			n := runtime.Stack(b, true)
			b = b[:n]
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

	log.SetOutput(flog)
	// hereafter use log for messages

	// uses cookies to hold sessionID
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // noerror

	fullfilename := filepath.Clean(*file)

	for index := 0; index < count; index++ {

		err := SendAFile(bindAddress, fullfilename, jar)

		if err == nil {
			break
		}
		log.Println(err)
		//DEBUG!!! time to run server for debuging
		time.Sleep(time.Second * 20)
		// continue on expected errors. Server not ready, timeouts ...
	}
	log.Println("uploader exited.")
}
