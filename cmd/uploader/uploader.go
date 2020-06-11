package main

// substitution is
// curl.exe -v -X POST 'http://127.0.0.1:64000/upload/zahar?&Filename="sendfile.rar"' -T .\testbackups\sendfile.rar --anyauth --user zahar
import (
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/zavla/dpapi"
	Error "github.com/zavla/upload/errstr"
	"github.com/zavla/upload/httpDigestAuthentication"

	"github.com/zavla/upload/fsdriver"
	"github.com/zavla/upload/logins"
	"github.com/zavla/upload/uploadclient"

	"flag"

	"context"
	"log"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"sync"

	"golang.org/x/net/publicsuffix"
)

var where uploadclient.ConnectConfig
var gitCommit string

const constRealm = "upload" // this is for http digest authantication predefined realm

func loadPemFileIntoCertPool(certpool *x509.CertPool, filename string) error {
	if certpool == nil {
		return errors.New("certpool is nil")
	}

	_, errCertPub := os.Stat(filename)

	if os.IsNotExist(errCertPub) {
		return errCertPub
	}

	pemCerts, err := ioutil.ReadFile(filename)
	if err == nil {
		ok := certpool.AppendCertsFromPEM(pemCerts)
		if !ok {
			return errors.New("failed to add a certificate to the pool of certificates")
		}
	}
	return nil

}

func main() {
	const op = "main()"
	// It is used to store passwords in a file.
	// TODO(zavla): params should be case insensitive
	paramLogname := flag.String("log", "", "a log `file`.")
	paramFile := flag.String("file", "", "a `file` you want to upload.")
	paramDirtomonitor := flag.String("dir", "", "a `directory` you want to upload.")
	username := flag.String("username", "", "a user `name` of an Upload service.")
	uploadServerURL := flag.String("service", `https://127.0.0.1:64000/upload`, "`URL` of the Upload service: https://..., ftp://....")
	//askpassword := flag.Bool("askpassword", true, "will ask a user `password` for the Upload service.")
	paramPasswordfile := flag.String("passwordfile", "", "a `file` with password.")
	paramCAcert := flag.String("cacert", "", "a file with CA public `certificate` that singed service's certificate (e.x. 'mkcertCA.pem')")
	savepassword := flag.Bool("savepassword", false, "save a password to a file specified with passwordfile.")
	savepasswordHTTPdigest := flag.Bool("savepasswordHTTPdigest", false, "save a HTTP digest of password to a file specified with passwordfile.")
	paramSkipCertVerify := flag.Bool("skipcertverify", false, "skips cert verification (if peer cert is self signed).")
	paramVersion := flag.Bool("version", false, "print `version`")
	paramSkipMarkAsUploaded := flag.Bool("skipmarkAsUploaded", false, "Skips marking of a file as uploaded.")
	flag.Parse()
	if *paramVersion {
		fmt.Printf("version: %s", gitCommit)
		os.Exit(0)
	}
	if len(os.Args[1:]) == 0 {
		flag.PrintDefaults()
		os.Exit(1)
		return
	}
	// check required parameters
	where.DontUseFileAttribute = *paramSkipMarkAsUploaded
	where.ToURL = *uploadServerURL
	where.InsecureSkipVerify = *paramSkipCertVerify
	if *paramFile == "" && *paramDirtomonitor == "" && !*savepassword && !*savepasswordHTTPdigest {
		log.Printf("-file or -dir must be specified.\n")
		os.Exit(1)
		return
	}
	if *savepassword && *paramPasswordfile == "" {
		log.Println("-passwordfile is not specified.")
		os.Exit(1)
		return
	}
	// clean user input paths
	var logfile, file, dirtomonitor, passwordfile, cacert string = "", "", "", "", ""
	if *paramFile != "" {
		file, _ = filepath.Abs(*paramFile)
	}
	if *paramDirtomonitor != "" {
		dirtomonitor, _ = filepath.Abs(*paramDirtomonitor)
	}
	if *paramPasswordfile != "" {
		passwordfile, _ = filepath.Abs(*paramPasswordfile)
	}
	if *paramLogname != "" {
		logfile, _ = filepath.Abs(*paramLogname)
	}
	if *paramCAcert != "" {
		cacert, _ = filepath.Abs(*paramCAcert)
	}

	// Stack print on panic
	defer func() {
		// on panic we will write to log file
		if err := recover(); err != nil {

			log.Printf("uploader main has paniced:\n%s\n", err)
			b := make([]byte, 2500) // enough buffer for stack trace text
			n := runtime.Stack(b, true)
			b = b[:n]
			// output stack trace to a log
			log.Printf("%d bytes of stack trace.\n%s\n", n, string(b))
		}
	}()

	// Setup log destination: stdout or user specified file.
	var flog *os.File
	if logfile == "" {
		flog = os.Stdout
	} else {

		var err error

		flog, err = os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0)
		if err != nil {
			log.Printf("%s\n", Error.E(op, err, errCantOpenFileForReading, 0, logfile))
			os.Exit(5)
			return
		}
	}

	// from here use a log for messages
	log.SetOutput(flog)
	defer func() { _ = flog.Close() }()

	// passwords only if there is a specified user
	if *username != "" {
		where.Username = *username

		loginsSt := logins.Logins{}
		// open a file or db with logins
		err := loginsSt.OpenDB(passwordfile)
		if err != nil {
			log.Printf("%s\n", err)
			os.Exit(1)
			return
		}
		if *savepassword && *savepasswordHTTPdigest {
			log.Println("Password must be saved either with 'savepassword' or with 'savepasswordHTTPdigest'. Not both.")
			os.Exit(1)
			return
		}
		if *savepassword {
			savepasswordWithDPAPI(&loginsSt, *username, false, constRealm)
			return
		}
		if *savepasswordHTTPdigest {
			savepasswordWithDPAPI(&loginsSt, *username, true, constRealm)
			return
		}

		loginFromFile, _, err := loginsSt.Find(where.Username, false)
		if err != nil {
			log.Printf("Login '%s' is not found in logins file %s\n", where.Username, passwordfile)
			os.Exit(1)
			return
		}
		passwordhashBytes := make([]byte, hex.DecodedLen(len(loginFromFile.Passwordhash)))

		hex.Decode(passwordhashBytes, []byte(loginFromFile.Passwordhash))
		decryptedPasswordHash, err := dpapi.Decrypt(passwordhashBytes)
		if err != nil {
			log.Printf("Password decryption from DPAPI failed: %s", err)
			os.Exit(1)
			return
		}
		where.PasswordHash = string(decryptedPasswordHash)

	} /* else there is no password */

	serviceUrl, err := url.ParseRequestURI(where.ToURL)

	if err != nil {
		// is needed to test an error in where.ToURL early. next use of Scheme is a worker()
		log.Printf("-listenTo parameter is bad: %s\n", err)
		os.Exit(1)
		return
	}
	println("schema == " + serviceUrl.Scheme)

	requireCAcert := false
	if serviceUrl.Scheme == "https" {
		// add username to URL for HTTPS
		if where.ToURL[len(where.ToURL)-1] != '/' {
			where.ToURL += "/"
		}
		if !strings.HasSuffix(where.ToURL, "upload/") {
			log.Printf("You must add /upload/ to the end of the -service parameter.\n")
			os.Exit(1)
			return
		}
		where.ToURL += *username

		requireCAcert = true
		if *paramCAcert == "" {
			log.Printf("You must use 'cacert' parameter and specify a file with CA certificate. Your service is %s\n", serviceUrl.Scheme)
			os.Exit(1)
			return
		}
	}

	// load CA certificate that signed the service's certificate.
	certpool := x509.NewCertPool()
	err = loadPemFileIntoCertPool(certpool, cacert)
	if err != nil && requireCAcert {
		log.Printf("A file with CA certificate must exist %s: %s\n", cacert, err)
		os.Exit(1)
		return
	}

	// TODO(zavla): load user certificate
	//var certSl []tls.Certificate
	// if *paramuserCert != "" {
	// 	pemCerts, err := ioutil.ReadFile(certFile)
	// 	if err == nil {
	// 		pBlock, _ := pem.Decode(pemCerts)
	// 		if pBlock == nil {
	// 			log.Printf("PEM encoding of service Certificate read from file %s are bad.", certFile)
	// 		} else {
	// 			serviceCA, err := x509.ParseCertificate(pBlock.Bytes)
	// 			if err != nil {
	// 				log.Printf("Parse of CA Certificate from file %s failed.", certFile)
	// 			} else {
	// 				certSl = []tls.Certificate{tls.Certificate{Leaf: serviceCA}}
	// 			}
	// 		}
	// 	}
	// }
	//where.Certs = certSl

	where.CApool = certpool // CA == certification authority that signed the service's certificate.

	// chNames is a channel with filenames to upload
	chNames := make(chan string, 2)

	if file != "" {
		// send to chNames
		chNames <- file
	}
	if dirtomonitor != "" {
		// walk a dir, send names to chNames
		go getFilenames(dirtomonitor, chNames) // closes chNames after adding all files
	} else {
		close(chNames)
	}
	// TODO(zavla): import "github.com/fsnotify/fsnotify"
	// TODO(zavla): do not mix POST request with URL parametres!

	mainCtx, callmeToCancel := context.WithCancel(context.Background())

	runWorkers(mainCtx, callmeToCancel, chNames)
	// Any worker may cancel other workers.
	// Here we are when all Workers have finished.

	log.Println("Normal exit.")
}

// runWorkers starts payload goroutines.
// Every gorouting is then allowed to cancel the whole request after encouning an athorization error.
func runWorkers(oneForAllCtx context.Context, callmeToCancel context.CancelFunc, chNames chan string) {
	var wg sync.WaitGroup
	be1 := true
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go worker(oneForAllCtx, callmeToCancel, &wg, chNames)

		// pause after first worker has been run, this allows to see if you have the right password
		if be1 {

			time.Sleep(time.Millisecond * 200)
			be1 = false
		}
	}
	wg.Wait()

}

// worker takes files names from channel and sends files.
// These peticular workers ARE allowed to cancel the whole context oneForAllCtx in case of authorization error.
func worker(oneForAllCtx context.Context, callmeToCancel context.CancelFunc, wg *sync.WaitGroup, ch chan string) {
	defer wg.Done()

	for name := range ch {
		select {
		case <-oneForAllCtx.Done():
			log.Println("cancel signal recieved")
			return
		default:
		}
		err := prepareAndSendAFile(oneForAllCtx, name, &where)
		if errError, ok := err.(*Error.Error); ok && errError.Code == uploadclient.ErrAuthorizationFailed {
			callmeToCancel()
			log.Printf("cancelling the whole request because the service responed 'Authorization failed'\n")

			return
		}
	}
	return
}

// prepareAndSendAFile works in a worker goroutine.
// It compute SHA1 of a file and calls uploadclient.SendAFile().
// At the end it calls markFileAsUploaded().
func prepareAndSendAFile(ctx context.Context, filename string, config *uploadclient.ConnectConfig) error {
	const op = "uploader.prepareAndSendAFile()"
	// uses cookies to hold sessionId
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // never error

	fullfilename := filepath.Clean(filename)
	storagepath := filepath.Dir(fullfilename)
	name := filepath.Base(fullfilename)

	//compute SHA1 of a file
	bsha1, err := fsdriver.GetFileSha1(storagepath, name)
	if err != nil {
		log.Printf("%s\n", Error.E(op, err, errCantOpenFileForReading, 0, ""))
		return err
	}

	serviceURL, _ := url.ParseRequestURI(config.ToURL) // ignore error because we test in early in main()
	if serviceURL.Scheme == "ftp" || serviceURL.Scheme == "ftps" {
		err = uploadclient.FtpAFile(ctx, config, fullfilename, bsha1)
	} else {
		err = uploadclient.SendAFile(ctx, config, fullfilename, jar, bsha1)
	}

	if err == nil {
		log.Printf("Upload successfull: %s\n", fullfilename)

		if !config.DontUseFileAttribute {
			if err := markFileAsUploaded(fullfilename); err != nil {
				// a non critical error
				log.Printf("%s\n", Error.E(op, err, errMarkFileFailed, 0, ""))
			}
		}
		// SUCCESS
		return nil
	}

	log.Printf("while sending the file %s - error: %s\n", fullfilename, err)
	return err // every error is returned to caller, including authorization error.
}

// getFilenames collects files names in a directory and sends them to channel chNames.
// If "archive" file attribute on Windows and FS_NODUMP_FL file attribute on linux is set
// then the file will be chosen.
func getFilenames(dir string, chNames chan<- string) {
	const op = "uploader.getFilenamesToupload()"
	defer close(chNames)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() {
			if dir == path { //first file
				return nil
			}
			println("skipping ", path)
			return filepath.SkipDir // no reqursion
		}
		// uses "archive" attribute on Windows and FS_NODUMP_FL file attribute on linux.
		isarchiveset := true
		if !where.DontUseFileAttribute {

			isarchiveset, _ = getArchiveAttribute(path)
		}
		if isarchiveset {
			chNames <- path
		}
		return nil // next file please
	})
	if err != nil {
		close(chNames)
		log.Printf("%s\n", Error.E(op, err, errReadingDirectory, 0, ""))
	}

	return
}

// savepasswordWithDPAPI asks for password, uses DPAPI to encrypt it, uses logins.Manager.Save() to store it.
// If usedInHTTPDigest == true additinally transforms password into HTTP digest hash form: username,realm, password.
func savepasswordWithDPAPI(loginsmanager logins.Manager, username string, usedInHTTPDigest bool, realm string) {
	const op = "uploader.savepasswordWithDPAPI"
	password, err := logins.AskPassword(username)
	if err != nil {
		log.Printf("Asking password failed: %s\n", err)
		return
	}

	loginobj := logins.Login{Login: username}

	hashUsernameRealmPassword := string(password)

	if usedInHTTPDigest {
		hashUsernameRealmPassword = httpDigestAuthentication.HashUsernameRealmPassword(loginobj.Login, realm, string(password))
	}
	DPAPIpasswordBytes, err := dpapi.Encrypt([]byte(hashUsernameRealmPassword))
	if err != nil {
		log.Printf("%s", Error.E(op, err, errDPAPIfailed, 0, ""))
		return
	}

	DPAPIpasswordText := make([]byte, hex.EncodedLen(len(DPAPIpasswordBytes)))
	_ = hex.Encode(DPAPIpasswordText, DPAPIpasswordBytes)
	_, err = loginsmanager.Add(loginobj.Login, "", string(DPAPIpasswordText))
	if err != nil {
		log.Printf("%s", err)
		return
	}
	err = loginsmanager.Save()
	if err != nil {
		log.Printf("Saving password file failed: %s\n", err)
		return
	}

	log.Printf("Password saved.\n")
	return

}

func savepasswordExit(loginsSt logins.Manager, username string) {
	loginobj := logins.Login{Login: username}
	err := logins.AskAndSavePasswordForHTTPDigest(loginsSt, loginobj, constRealm)
	if err != nil {
		log.Printf("Saving password file failed: %s\n", err)
		return
	}

	log.Printf("Password saved.\n")
	return

}
