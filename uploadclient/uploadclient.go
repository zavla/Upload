/*
Package uploadclient contains functions for uploading one file to different types of service.
*/
package uploadclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"

	"encoding/hex"
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

	Error "github.com/zavla/upload/errstr"

	"github.com/zavla/upload/httpDigestAuthentication"
	"github.com/zavla/upload/liteimp"

	"github.com/google/uuid"
	"github.com/secsy/goftp"
)

// ConnectConfig holds connection parameters: URL and username etc..
type ConnectConfig struct {
	// ToURL hold a connection URL to a service
	ToURL string

	Password string // TODO(zavla): check the cyrillic passwords
	// OR you may specify a hash
	PasswordHash string // one may already store a hash of password
	Username     string

	// Certs is used in TLSConfig for the client to identify itself.
	Certs []tls.Certificate

	// CApool holds your CA certs that this connection will use to verify its peer.
	CApool *x509.CertPool // for the client to check the servers certificates

	// InsecureSkipVerify is used in x509.TLSConfig to skip chain verification.
	// Do not use in production
	InsecureSkipVerify bool

	// DontUseFileAttribute allows scheme where there are two services that recieves the same files simultaniously.
	// But one service is a master.
	DontUseFileAttribute bool
}

func redirectPolicyFunc(_ *http.Request, _ []*http.Request) error {

	return nil
}

// FtpAFile send file to a ftp server.
func FtpAFile(ctx context.Context, where *ConnectConfig, fullfilename string, bsha1 []byte) error {
	const op = "uploadclient.FtpAFile"
	// TODO(zavla): make use of connections pool
	serviceURL, _ := url.ParseRequestURI(where.ToURL) // error checked early already in main

	confFtp := goftp.Config{
		User:     where.Username,
		Password: where.PasswordHash,
		TLSConfig: &tls.Config{
			ClientCAs:          where.CApool,
			ServerName:         serviceURL.Hostname(),
			InsecureSkipVerify: where.InsecureSkipVerify,
		},

		TLSMode:    goftp.TLSExplicit,
		IPv6Lookup: false,
	}

	cftp, err := goftp.DialConfig(confFtp, serviceURL.Host)
	if err != nil {
		log.Printf("gotfp.DialConfig returned an error: %s\r\n", err)
		return Error.E(op, err, errFtpDialFailed, 0, "")
	}
	defer cftp.Close()

	// local file info
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0400)
	if err != nil {
		return Error.E(op, err, errCantOpenFileForReading, 0, "")
	}
	defer f.Close() // ignore err because f opened RO
	finfo, err := os.Stat(fullfilename)
	if err != nil {
		log.Printf("os.Stat failed with error: %s\r\n", err)
		return err
	}

	// remote file info
	remotefilename := filepath.Base(fullfilename)
	remotefinfo, err := cftp.Stat(remotefilename)
	if err != nil {
		//log.Printf("goftp.Stat failed with: %s", err)
	}

	if remotefinfo != nil && remotefinfo.Size() == finfo.Size() {
		// lets do not send files again, rely on filesize
		return Error.E(op, nil, errServerForbiddesUpload, 0, "")
	}

	// send a file to ftp
	err = cftp.Store(remotefilename, f)
	if err != nil {
		log.Printf("gotfp.Store returned an error: %s\r\n", err)
		log.Printf("%s\r\n", err)
		return Error.E(op, err, errFtpDialFailed, 0, "")

	}
	dirsha1 := ".sha1"
	err = ftpDirExistsOrCreateDir(cftp, dirsha1)
	if err != nil {
		log.Printf("goftp.MkDir failed: %s\r\n", err)
	}
	if err == nil {
		// additinally send a file with sha1 in filename
		ssha1 := make([]byte, hex.EncodedLen(len(bsha1)))
		_ = hex.Encode(ssha1, bsha1)
		pbf := bytes.NewBuffer([]byte{})
		err = cftp.Store(filepath.Join(".sha1", remotefilename+".sha1_"+string(ssha1)), pbf)
		if err != nil {
			log.Printf("gotftp.Store failed: %s\r\n", err)

		}
	}
	// no error
	return nil
}

func ftpDirExistsOrCreateDir(cftp *goftp.Client, dir string) error {
	_, err := cftp.Stat(dir)
	if err != nil {
		if errftp, ok := err.(goftp.Error); ok {

			if strings.Contains(errftp.Message(), "The system cannot find") {
				_, err = cftp.Mkdir(dir)
				if err != nil {
					return err
				}
				// ok, created
				return nil
			}
		}
		return err
	}
	// ok, exist
	return nil
}

// SendAFile sends file to a service Upload.
// jar holds cookies from server http.Responses and use them in http.Requests
func SendAFile(ctx context.Context, where *ConnectConfig, fullfilename string, jar *cookiejar.Jar, bsha1 []byte) error {
	// I use op as the first argument to Error.E()
	const op = "uploadclient.sendAFile()"

	// opens the file
	_, name := filepath.Split(fullfilename)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0400)
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

	reqctx, _ := context.WithTimeout(ctx, 3*time.Hour)
	// creates http.Request
	req, err := http.NewRequestWithContext(reqctx, "POST", where.ToURL, nil)

	if err != nil {
		return Error.E(op, err, errCantCreateHTTPRequest, 0, "")
	}
	// use context to define timeout of total http.Request
	// ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	// req = req.WithContext(ctx)

	req.Header.Add("Expect", "100-continue")   // Client will not send body at once, it will wait for server response status "100-continue"
	req.Header.Add("Connection", "keep-alive") // We have at least two roundprtips for authorization
	// no connection with 'file already closed' req.Header.Add("Accept-Encoding", "deflate, compress, gzip;q=0, identity") //
	req.Header.Add("sha1", fmt.Sprintf("%x", bsha1))

	query := req.URL.Query()
	query.Add("filename", name) // url parameter &filename
	req.URL.RawQuery = query.Encode()

	// Use TLS if caller supplied us with root CA certificates and optional user certificates.
	var tlsConf *tls.Config
	if where.CApool != nil {
		tlsConf = &tls.Config{
			RootCAs: where.CApool,
		}

		if where.Certs != nil {
			tlsConf.Certificates = where.Certs
		}

	}

	// I use transport to define timeouts: idle and expect timeout
	tr := &http.Transport{
		TLSClientConfig: tlsConf,
		Dial: func(network, addr string) (net.Conn, error) {
			conn, err := net.DialTimeout(network, addr, 4*time.Second) // timeout to reach the server
			return conn, err
		},
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 7 * time.Hour,    // wait for headers for how long
		TLSHandshakeTimeout:   5 * time.Second,  // time to negotiate for TLS
		IdleConnTimeout:       5 * time.Minute,  // server responded but connection is idle for how long
		ExpectContinueTimeout: 10 * time.Second, // expects response status 100-continue before sending the request body
	}
	// use http.Client to define cookies jar and transport usage
	cli := &http.Client{
		CheckRedirect: redirectPolicyFunc,

		Timeout: 0,
		// Timeout > 0 do not allow client to upload big files.
		// Timeout - we connected to ip port but didn't manage to read the whole response (headers and body) within Timeout
		// I set timeout in every http.Request in the code above
		Transport: tr,  // I don't use http.DefaultTransport
		Jar:       jar, // http.Request uses jar to keep cookies (to hold sessionID)
	}

	waitBeforeRetry := time.Duration(10) * time.Second
	const constNumberOfRetries = 120
	//waitForFileToBecomeAvailable := time.Duration(24) * time.Hour

	ret := Error.E(op, err, errNumberOfRetriesExceeded, 0, "")
	authorizationsent := false
	// we expect from server to send a hash of passwordhash and our Response HTTP digest authorization header.
	oneResponseFromServerHasAProveOfRightPasswordhash := false
	savedProveServerHasRightPasswordhash := ""

	log.Printf("sending %s\r\n", fullfilename)
	// currentfilestatus holds last sent state, on error client retries again with this file state.
	// It depends on error kind: transport may have sent some part of a file or may not.
	currentfilestatus := liteimp.JsonFileStatus{JsonResponse: liteimp.JsonResponse{Count: filesize}}
	// cycle for some errors that can be tolerated
	for i := 0; i < constNumberOfRetries; i++ {

		// first we check a cancel signal
		select {
		case <-ctx.Done(): // cancel signal
			ret = Error.E(op, nil, errCanceled, 0, "")
			log.Printf("%s\r\n", ret)
			return ret
		default:
		}
		// every step makes a request with a new body bytes (or continues upload)

		// The first client request goes without a cookie.
		// We get the cookies from server and other requests come with sessionID in cookies.

		resp, err := cli.Do(req) // req sends a file f in the body.
		// ATTENTION: cli.Do() closes the _REQUEST_ body (the file f).
		// We are supposed to read to the EOF AND close the _RESPONSE_ body but only if err == nil
		// (when err!=nil RESPONSE body is closed by Do() itself)

		_, ok := req.Header["Cookie"]
		if ok {
			req.Header["Cookie"] = []string{} // clears because cookies are in cli.Jar and are copied from Jar into every http.Request by cli.Do()
		}

		req.Body = http.NoBody // lets ensure no file will be transfered on next request unless explicitly arainged by "req.Body = f"

		if err != nil || resp == nil {
			// here response body is already closed by underlying http.Transport
			// response may be nil when transport fails with timeout (it may timeout while i am debugging the upload server)
			if urlErr, ok := err.(*url.Error); ok && urlErr.Timeout() {
				// err by timeout on client side. That is http.Client() fired a timeout.
				ret = Error.E(op, err, errCanceled, 0, "http.Client() fired a timeout.")

			} else {
				ret = Error.E(op, err, errWhileSendingARequestToServer, 0, "")
			}
			log.Printf("%s\r\n", ret)

			time.Sleep(waitBeforeRetry) // waits: can't connect

			if oneResponseFromServerHasAProveOfRightPasswordhash == true {
				f, err = openandseekRO(fullfilename, currentfilestatus.Startoffset)
				if err != nil {
					// file may appear again any time and we will continue upload
					// TODO(zavla): save filename to queue!!!

					ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
					log.Printf("%s\r\n", ret)
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
		req.ContentLength = 0
		// DEBUG !!! //log.Printf("%s", resp.Status)

		// Here client extracts a prove from server that it has the right password hash.
		// Any response from server will hold a prove.
		if authorizationsent {
			aprove := resp.Header.Get(httpDigestAuthentication.KeyProvePeerHasRightPasswordhash)
			if aprove != "" && savedProveServerHasRightPasswordhash == aprove {
				oneResponseFromServerHasAProveOfRightPasswordhash = true
			}
		}
		liteimp.Debugprint("%s\n", resp.Status)
		//-------decoding status
		if resp.StatusCode == http.StatusAccepted {
			if oneResponseFromServerHasAProveOfRightPasswordhash {

				return nil // upload completed successfully
			}
			return Error.E(op, nil, errServerDidntProveItHasPasswordhash, Error.ErrKindInfoForUsers, "")
		}
		if resp.StatusCode == http.StatusForbidden {
			// server actively denies upload. Changes to the file are forbidden.
			return Error.E(op, nil, errServerForbiddesUpload, Error.ErrKindInfoForUsers, tomsg(bodybytes))
		}
		if resp.StatusCode == http.StatusUnauthorized && authorizationsent {
			log.Printf("Username or password is incorrect.\r\n")
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

			// TODO(zavla): decide if this IF is needed
			// if oneResponseFromServerHasAProveOfRightPasswordhash {
			// 	f, err = openandseekRO(fullfilename, currentfilestatus.Startoffset)
			// 	if err != nil {
			// 		// file may appear again any time and we will continue upload
			// 		// TODO(zavla): save filename to queue!!!

			// 		ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
			// 		log.Printf("%s", ret)
			// 		return ret
			// 	}
			// 	req.Body = f
			// 	req.ContentLength = currentfilestatus.Count // file size
			// }

			continue // this time with Authorization cookie

		}
		// DEBUG !!!
		//time.Sleep(1 * time.Second)

		if resp.StatusCode == http.StatusConflict { // we expect StatusConflict, it means we are to continue upload.
			// server responded "a file already exists" with JSON
			filestatus, err := decodeJSONinBody(bodybytes)

			if err != nil {
				// logs incorrect server respone

				ret = Error.E(op, err, errServerRespondedWithBadJSON, 0, "")
				log.Printf("%s\r\n", ret)
				return ret // do not retry, just return
			}
			// server sent a proper json response

			startfrom := filestatus.Startoffset

			f, err = openandseekRO(fullfilename, startfrom) // garanties starfrom to be a new offset

			if err != nil {
				ret = Error.E(op, err, errFileSeekErrorOffset, 0, "")
				log.Printf("%s\r\n", ret)
				return ret // do not retry if we can't open the file, just return
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
			if currentfilestatus.Startoffset != 0 {
				log.Printf("%s: continue from startoffset %d for file %s\r\n", op, currentfilestatus.Startoffset, name)
			}
			// no delay, do expected request again
			continue // cycles to next cli.Do()

		}

		{
			if !oneResponseFromServerHasAProveOfRightPasswordhash {
				log.Printf(Error.I18text("Server side didn't prove it has the right password hash.\r\n"))
			}
			log.Printf(Error.I18text("upload service responded with HTTP status %s, the response body was %s\r\n", resp.Status, tomsg(bodybytes)))

		}
		// here goes other errors and http.statuses:
		// upload failed for some reason maybe timeouted. Lets retry with current cookies.
		time.Sleep(waitBeforeRetry)

	}
	return ret

}
func openandseekRO(fullfilename string, offset int64) (*os.File, error) {
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0400)
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

func tomsg(b []byte) string {
	l := len(b)
	if l > 2000 {
		l = 2000
	}
	return string(b[:l])
}
