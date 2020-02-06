/*
Package uploadclient contains a client side of this upload service.
*/
package uploadclient

import (
	"context"
	"io"

	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	Error "upload/errstr"

	"upload/httpDigestAuthentication"
	"upload/liteimp"

	"github.com/google/uuid"
)

// ConnectConfig is used to specify URL and username.
type ConnectConfig struct {
	ToURL    string
	Password string // TODO(zavla): check the cyrillic passwords
	// OR you may specify a hash
	PasswordHash string // one may already store a hash of password
	Username     string
}

func redirectPolicyFunc(_ *http.Request, _ []*http.Request) error {

	return nil
}

// SendAFile sends file to a service Upload.
// jar holds cookies from server http.Responses and use them in http.Requests
func SendAFile(ctx context.Context, where *ConnectConfig, fullfilename string, jar *cookiejar.Jar, bsha1 []byte) error {
	// I use op as the first argument to Error.E()
	const op = "uploadclient.sendAFile()"

	// opens the file
	_, name := filepath.Split(fullfilename)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {
		// let it be an error because the first time we send a file we should know its size.
		// Later, when we can't _resume_ upload we will wait for the file to appear. May be a disk will be attached.
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
	req, err := http.NewRequest("POST", where.ToURL, f)
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
	req.Header.Add("Accept-Encoding", "deflate, compress, gzip;q=0, identity") //

	query := req.URL.Query()
	query.Add("filename", name) // url parameter &filename
	req.URL.RawQuery = query.Encode()

	// I use transport to define timeouts: idle and expect timeout
	tr := &http.Transport{

		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 10 * time.Second, // wait for headers for how long
		TLSHandshakeTimeout:   15 * time.Second, // time to negotiate for TLS
		IdleConnTimeout:       5 * time.Minute,  // server responded but connection is idle for how long
		ExpectContinueTimeout: 20 * time.Second, // expects response status 100-continue before sending the request body
	}

	// use http.Client to define cookies jar and transport usage
	cli := &http.Client{
		CheckRedirect: redirectPolicyFunc,

		Timeout: 0,
		// Timeout > 0 do not allow client to upload big files.
		//Timeout:   5 * time.Minute, // we connected to ip port but didn't manage to read the whole response (headers and body) within Timeout
		Transport: tr,  // I don't use http.DefaultTransport
		Jar:       jar, // http.Request uses jar to keep cookies (to hold sessionID)
	}

	waitBeforeRetry := time.Duration(10) * time.Second
	const constNumberOfRetries = 20
	//waitForFileToBecomeAvailable := time.Duration(24) * time.Hour

	ret := Error.E(op, err, errNumberOfRetriesExceeded, 0, "")
	authorizationsent := false

	log.Printf("%s  ->\n", fullfilename)
	// cycle for some errors that can be tolerated
	for i := 0; i < constNumberOfRetries; i++ {

		// first we check to see a cancel signal
		select {
		case <-ctx.Done(): // cancel signal
			ret = Error.E(op, nil, errCanceled, 0, "")
			log.Printf("%s", ret)
			return ret
		default:
		}
		// every step makes a request with a new body bytes (continues upload)
		req.Body = f
		// the first client request goes without a cookie.
		// We get the cookies from serverv and other requests come with sessionID in cookies.

		resp, err := cli.Do(req) // req sends a file f in the body.
		// ATTENTION: cli.Do() closes the _REQUEST_ body (file f).
		// We are supposed to read to the EOF AND close the _RESPONSE_ body but only if err == nil
		// (when err!=nil RESPONSE body is closed by Do())

		if err != nil || resp == nil {
			// here response body is already closed by underlying http.Transport
			// response may be nil when transport fails with timeout (it may timeout while i am debugging the upload server)
			if urlErr, ok := err.(*url.Error); ok && urlErr.Timeout() {
				// err by timeout on client side. That is http.Client() fired a timeout.
				ret = Error.E(op, err, errCanceled, 0, "http.Client() fired a timeout.")

			} else {
				ret = Error.E(op, err, errWhileSendingARequestToServer, 0, "")
			}
			log.Printf("%s", ret)

			time.Sleep(waitBeforeRetry) // waits: can't connect

			// opens file again, file may be unavailable, wait for it.
			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
				log.Printf("%s", ret)
				// file may appear again any time and we will continue upload
				// TODO(zavla): save filename to queue!!!

			}

			continue // retries
		}
		// on err!=nil we must read and close the response body.
		bodylen, errbodylen := strconv.Atoi(resp.Header.Get("Content-Length"))
		if errbodylen != nil {
			_ = resp.Body.Close()
			return Error.E(op, errbodylen, 0, 0, "Content-Length in response is bad.")
		}
		bodybytes := make([]byte, bodylen)
		if bodylen != 0 {
			nbytes, errbodyread := io.ReadFull(resp.Body, bodybytes)
			if errbodyread != nil || nbytes != bodylen {
				_ = resp.Body.Close()
				return Error.E(op, errbodyread, 0, 0, "Can't read from response body.")
			}
		}
		_ = resp.Body.Close() // A MUST to free resource descriptor

		//-------decoding status
		if resp.StatusCode == http.StatusAccepted {
			return nil // upload completed successfully
		}
		if resp.StatusCode == http.StatusForbidden {
			// server actively denies upload. Changes to the file are forbidden.
			return Error.E(op, nil, errServerForbiddesUpload, Error.ErrKindInfoForUsers, "")
		}
		if resp.StatusCode == http.StatusUnauthorized && authorizationsent {
			log.Printf("Username or password is incorrect.")
			return Error.E(op, nil, ErrAuthorizationFailed, 0, "")
		}
		if resp.StatusCode == http.StatusUnauthorized {
			wwwauthstr := resp.Header.Get("WWW-Authenticate")
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
			challengeAndCredentials.URI = req.URL.RawQuery
			challengeAndCredentials.NonceCount = "00000001"

			// next lets construct a 'response' parameter for the HTTP Authorization cookie.
			hashUsernameRealmPassword := ""
			if where.Password != "" {
				hashUsernameRealmPassword = httpDigestAuthentication.HashUsernameRealmPassword(where.Username, challengeAndCredentials.Realm, where.Password)
			} else {
				hashUsernameRealmPassword = where.PasswordHash
			}
			responseParam, err := httpDigestAuthentication.GenerateResponseAuthorizationParameter(hashUsernameRealmPassword, challengeAndCredentials)

			challengeAndCredentials.Username = where.Username
			challengeAndCredentials.Response = responseParam

			req.Header.Set("Authorization", httpDigestAuthentication.GenerateAuthorization(challengeAndCredentials))
			authorizationsent = true

			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
				log.Printf("%s", ret)
			}

			continue // this time with Authorization cookie

		}

		// next we read RESPONSE resp.Body and need to close it.

		// Open file again as we are going to do the next upload attempt.
		// The rest IFs are for expected "error" http.StatusConflicts and other upload service specific errors:
		// 		such as service messed something and asks us to retry.
		f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
		if err != nil {
			ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
			log.Printf("%s", ret)
			return ret
		}
		//log.Printf("Connected to %s", req.URL)
		if resp.StatusCode == http.StatusConflict { // we expect StatusConflict, it means we are to continue upload.
			// server responded "a file already exists" with JSON
			filestatus, err := decodeJSONinBody(bodybytes)

			if err != nil {
				// logs incorrect server respone

				ret = Error.E(op, err, errServerRespondedWithBadJSON, 0, "")
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
			req.ContentLength = bytesleft
			log.Printf("%s: continue from startoffset %d", op, newoffset)
			// no delay, do expected request again
			continue // cycles to next cli.Do()

		}

		{

			log.Printf("Debug msg: Upload service responded with HTTP status %s, the response body was %s", resp.Status, string(bodybytes))

		}
		// here goes other errors and http.statuses:
		// upload failed for some reason maybe timeouted. Lets retry with current cookies.
		time.Sleep(waitBeforeRetry)

	}
	return ret

}

// decodeJSONinBody tries to decode resp.Body as a JSON of type liteimp.JsonFileStatus.
// Doesn't close the resp.Body.
func decodeJSONinBody(bodybytes []byte) (value *liteimp.JsonFileStatus, err error) {

	const op = "uploader.decodeJSONinBody()"
	value = &liteimp.JsonFileStatus{}
	// unmarshal server json response
	if err := json.Unmarshal(bodybytes, &value); err != nil {
		ret := Error.E(op, err, errServerRespondedWithBadJSON, 0, "")
		return value, ret
	}
	return value, nil

}
