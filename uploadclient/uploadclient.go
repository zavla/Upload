/*
Package uploadclient contains a client side of this upload service.
*/
package uploadclient

import (
	"context"

	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
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
	Password string // one need to translate []byte to propper utf-8 string
	Username string
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
		// Later, when we can't _resume_ upload we will wait for the file to appear. May be disk will be attached.
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
	//waitForFileToBecomeAvailable := time.Duration(24) * time.Hour

	ret := Error.E(op, err, errNumberOfRetriesExceeded, 0, "")
	authorizationsent := false

	// cycle for some errors that can be tolerated
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
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
		// ATTENTION: cli.Do() _closes_ the REQUEST req.Body (file f).

		if err != nil || resp == nil {
			// response may be nil when transport fails with timeout (it may timeout while i am debugging the upload server)
			ret = Error.E(op, err, errCantConnectToServer, 0, "")
			log.Printf("%s", ret)

			time.Sleep(waitBeforeRetry) // waits: can't connect

			// opens file again, file may be unavailable, wait for it.
			f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			if err != nil {
				ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
				log.Printf("%s", ret)
				// file may appear again any time and we will continue upload
				// TODO(zavla): save filename to queue!!!

				// delayCtx, _ := context.WithTimeout(ctx, waitForFileToBecomeAvailable)
				// select {
				// case <-delayCtx.Done():
				// 	// delayCtx signalled
				// }
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
			// server actively denies upload. Changes to the file are forbidden.
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
			// next lets construct a 'response' parameter for the HTTP Authorization cookie.
			hashUsernameRealmPassword := httpDigestAuthentication.HashUsernameRealmPassword(where.Username, challengeAndCredentials.Realm, where.Password)
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
			log.Printf("DEBUG:Server responded with body: %s", string(b))
		}
		// here goes other errors and http.statuses:
		// upload failed for some reason maybe timeouted. Retry with current cookie.
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
