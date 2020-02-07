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
	filesize := stat.Size()

	err = f.Close() // explicit Close()
	if err != nil {
		return Error.E(op, err, errCantOpenFileForReading, 0, "")
	}

	// creates http.Request
	req, err := http.NewRequest("POST", where.ToURL, nil)
	// f will be closed after http.Client.Do
	if err != nil {
		return Error.E(op, err, errCantCreateHTTPRequest, 0, "")
	}
	// use context to define timeout of total http.Request
	// ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	// req = req.WithContext(ctx)

	// 'file already closed'???
	req.Header.Add("Expect", "100-continue")                                   // Client will not send body at once, it will wait for server response status "100-continue"
	req.Header.Add("Connection", "keep-alive")                                 // We have at least two roundprtips for authorization
	req.Header.Add("Accept-Encoding", "deflate, compress, gzip;q=0, identity") //
	req.Header.Add("sha1", fmt.Sprintf("%x", bsha1))

	query := req.URL.Query()
	query.Add("filename", name) // url parameter &filename
	req.URL.RawQuery = query.Encode()

	// I use transport to define timeouts: idle and expect timeout
	tr := &http.Transport{

		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 7 * time.Hour,    // wait for headers for how long
		TLSHandshakeTimeout:   15 * time.Second, // time to negotiate for TLS
		IdleConnTimeout:       5 * time.Minute,  // server responded but connection is idle for how long
		ExpectContinueTimeout: 10 * time.Second, // expects response status 100-continue before sending the request body
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
	// we expect from server to send a hash of passwordhash and our Response HTTP digest authorization header.
	oneResponseFromServerHasAProveOfRightPasswordhash := false
	savedProveServerHasRightPasswordhash := ""

	log.Printf("%s  ->\n", fullfilename)
	// currentfilestatus holds last sent state, on error client retries again with this file state.
	// It depends on error kind: transport may have sent some part of a file or may not.
	currentfilestatus := liteimp.JsonFileStatus{JsonResponse: liteimp.JsonResponse{Count: filesize}}
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
		// every step makes a request with a new body bytes (or continues upload)
		// !!!req.Body = f // this somehow causes 'file already closed' on cli.Do()

		// The first client request goes without a cookie.
		// We get the cookies from server and other requests come with sessionID in cookies.

		resp, err := cli.Do(req) // req sends a file f in the body.
		// ATTENTION: cli.Do() closes the _REQUEST_ body (the file f).
		// We are supposed to read to the EOF AND close the _RESPONSE_ body but only if err == nil
		// (when err!=nil RESPONSE body is closed by Do() itself)

		req.Body = nil // lets ensure no file will be transfered on next request unless explicitly aranged

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

			//req.Body = nil
			if oneResponseFromServerHasAProveOfRightPasswordhash {
				f, err = openandseekRO(fullfilename, currentfilestatus.Startoffset)
				if err != nil {
					// file may appear again any time and we will continue upload
					// TODO(zavla): save filename to queue!!!

					ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
					log.Printf("%s", ret)
					return ret
				}
				req.Body = f
				req.ContentLength = currentfilestatus.Count // file size
			}

			continue // retries
		}
		// on err!=nil we must read and close the response body.
		reqContentLengthstr := resp.Header.Get("Content-Length")

		bodybytes := make([]byte, 0) // init
		if reqContentLengthstr != "" {
			bodylen, errbodylen := strconv.Atoi(reqContentLengthstr)
			if errbodylen != nil {
				_ = resp.Body.Close()
				return Error.E(op, errbodylen, 0, 0, "Content-Length in response is bad.")
			}
			bodybytes = make([]byte, bodylen)
			if bodylen != 0 {
				nbytes, errbodyread := io.ReadFull(resp.Body, bodybytes) //  READs all the RESPONSE boby, this will allow transport to close connection.
				if errbodyread != nil || nbytes != bodylen {
					_ = resp.Body.Close()
					return Error.E(op, errbodyread, 0, 0, "Can't read from response body.")
				}
			}
		}
		_ = resp.Body.Close() // A MUST to free resource descriptor

		// extract prove from server of right password
		if authorizationsent {
			aprove := resp.Header.Get(httpDigestAuthentication.KeyProvePeerHasRightPasswordhash)
			if aprove != "" && savedProveServerHasRightPasswordhash == aprove {
				oneResponseFromServerHasAProveOfRightPasswordhash = true
			}
		}

		//-------decoding status
		if resp.StatusCode == http.StatusAccepted {
			if oneResponseFromServerHasAProveOfRightPasswordhash {

				return nil // upload completed successfully
			}
			return Error.E(op, nil, errServerDidntProveItHasPasswordhash, Error.ErrKindInfoForUsers, "")
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
			// this saved prove will be used later
			savedProveServerHasRightPasswordhash = httpDigestAuthentication.ProveThatPeerHasRightPasswordhash(hashUsernameRealmPassword, responseParam)

			challengeAndCredentials.Username = where.Username
			challengeAndCredentials.Response = responseParam

			req.Header.Set("Authorization", httpDigestAuthentication.GenerateAuthorization(challengeAndCredentials))
			authorizationsent = true

			// f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
			// if err != nil {
			// 	ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
			// 	log.Printf("%s", ret)
			// 	return ret
			// }
			// if oneResponseFromServerHasAProveOfRightPasswordhash {
			// 	req.Body = f
			// 	req.ContentLength = filesize // file size
			// }
			//---
			//req.Body = nil
			if oneResponseFromServerHasAProveOfRightPasswordhash {
				f, err = openandseekRO(fullfilename, currentfilestatus.Startoffset)
				if err != nil {
					// file may appear again any time and we will continue upload
					// TODO(zavla): save filename to queue!!!

					ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
					log.Printf("%s", ret)
					return ret
				}
				req.Body = f
				req.ContentLength = currentfilestatus.Count // file size
			}

			continue // this time with Authorization cookie

		}

		// Open file again as we are going to do the next upload attempt.
		// The rest IFs are for expected "error" http.StatusConflicts and other upload service specific errors:
		// 		such as service messed something and asks us to retry.
		// f, err = os.OpenFile(fullfilename, os.O_RDONLY, 0)
		// if err != nil {
		// 	ret = Error.E(op, err, errCantOpenFileForReading, 0, "")
		// 	log.Printf("%s", ret)
		// 	return ret
		// }

		if resp.StatusCode == http.StatusLengthRequired &&
			oneResponseFromServerHasAProveOfRightPasswordhash {
			// server has proved it has the right hash and complain that we haven't sent a file in the body
			// log.Printf("server complain we haven't send a file.")
			f, err = openandseekRO(fullfilename, currentfilestatus.Startoffset)
			if err != nil {
				ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
				log.Printf("%s", ret)
				return ret
			}
			req.Body = f
			req.ContentLength = currentfilestatus.Count // file size
			continue
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

			f, err = openandseekRO(fullfilename, startfrom) // garanties starfrom to be a new offset

			if err != nil {
				ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
				log.Printf("%s", ret)
				return ret // do not retry, just return
			}

			bytesleft := filesize - startfrom
			// update current state
			currentfilestatus.Startoffset = startfrom
			currentfilestatus.Count = bytesleft

			req.Body = f
			req.ContentLength = currentfilestatus.Count

			query = req.URL.Query()
			query.Set("startoffset", strconv.FormatInt(currentfilestatus.Startoffset, 10))
			query.Set("count", strconv.FormatInt(currentfilestatus.Count, 10))
			query.Set("filename", name)
			req.URL.RawQuery = query.Encode()
			log.Printf("%s: continue from startoffset %d", op, currentfilestatus.Startoffset)
			// no delay, do expected request again
			continue // cycles to next cli.Do()

		}

		{
			if !oneResponseFromServerHasAProveOfRightPasswordhash {
				log.Printf(Error.I18text("Server sider didn't prove it has a right password hash."))
			} else {
				log.Printf(Error.I18text("upload service responded with HTTP status %s, the response body was %s", resp.Status, string(bodybytes)))
			}

		}
		// here goes other errors and http.statuses:
		// upload failed for some reason maybe timeouted. Lets retry with current cookies.
		time.Sleep(waitBeforeRetry)

	}
	return ret

}
func openandseekRO(fullfilename string, offset int64) (*os.File, error) {
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	const from = 0 // from begin
	newoff, err := f.Seek(offset, from)
	if err != nil || newoff != offset {
		return nil, err
	}
	return f, nil

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
