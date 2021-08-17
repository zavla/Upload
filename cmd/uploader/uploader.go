package main

// substitution is
// curl.exe -v -X POST 'http://127.0.0.1:64000/upload/zahar?&Filename="sendfile.rar"' -T .\testbackups\sendfile.rar --anyauth --user zahar
import (
	"crypto/x509"
	"encoding/hex"
	"fmt"
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

// gitCommit holds version information by means of go build -ldflags="-X 'main.gitCommit=%gg%'"
var gitCommit string

const constRealm = "upload" // this is for http digest authantication predefined realm

// Run command example:
const exampleRun = `.\uploader.exe -service https://192.168.3.53:64002/upload -username zahar -dir .\testdata\testbackups\ -passwordfile ./testlogin.json -cacert ./mkcertCA.pem`

func main() {
	const op = "main()"
	// It is used to store passwords in a file.
	// TODO(zavla): params should be case insensitive
	// TODO(zavla): log file flush

	paramLogname := flag.String("log", "", "a log `file`.")
	paramFile := flag.String("file", "", "a `file` you want to upload.")
	paramDirtomonitor := flag.String("dir", "", "a `directory` you want to upload.")
	username := flag.String("username", "", "a `user` in Upload service.")
	uploadServerURL := flag.String("service", `https://127.0.0.1:64000/upload`, "`URL` of the Upload service: https://..., ftp://....")
	paramPasswordfile := flag.String("passwordfile", "", "a `file` with password.")
	paramCAcert := flag.String("cacert", "", "a PEM file with a CA public `certificate` that singed service's certificate")
	savepassword := flag.Bool("savepassword", false, "save a user password to a file specified with passwordfile.")
	forHttps := flag.Bool("forhttps", false, "to use for https.")
	paramSkipCertVerify := flag.Bool("skipcertverify", false, "skips cert verification (use it if service's cert is self signed).")
	paramVersion := flag.Bool("version", false, "print `version`")
	paramSkipMarkAsUploaded := flag.Bool("skipmarkAsUploaded", false, "Skips marking of a file as uploaded.")

	flag.Parse()
	if *paramVersion {
		fmt.Printf("version: %s\r\n", gitCommit)
		return
	}
	if len(os.Args[1:]) == 0 {
		flag.PrintDefaults()
		os.Exit(1)
		return
	}
	where.DontUseFileAttribute = *paramSkipMarkAsUploaded
	where.ToURL = *uploadServerURL
	where.InsecureSkipVerify = *paramSkipCertVerify

	// check required parameters: either 'file' or 'dir'
	if *paramFile == "" && *paramDirtomonitor == "" && !*savepassword && !*forHttps {
		log.Printf("-file or -dir must be specified.\r\n")
		os.Exit(1)
		return
	}

	// user asks to save a password - then check password file name
	if *savepassword && *paramPasswordfile == "" {
		log.Printf("-passwordfile is not specified.\r\n")
		os.Exit(1)
		return
	}

	// absolutize and clean input file paths
	var logfile, file, dirtomonitor, passwordfile, cacert string = "", "", "", "", ""
	inputFilenames := []*string{paramLogname, paramFile, paramDirtomonitor, paramPasswordfile, paramCAcert}

	if err := AbsInput(inputFilenames, &logfile, &file, &dirtomonitor, &passwordfile, &cacert); err != nil {
		log.Printf("A file name is incorrect after Abs(), %w\r\n", err)
		return
	}

	// Stack print on panic
	defer func() {
		// on panic we will write to log file
		if err := recover(); err != nil {

			log.Printf("PANIC: uploader has paniced:\r\n%s\r\n", err)
			b := make([]byte, 2500) // enough buffer for stack trace text
			n := runtime.Stack(b, true)
			b = b[:n]
			// output stack trace to a log
			log.Printf("%d bytes of stack trace.\r\n%s\r\n", n, string(b))
		}
	}()

	// Setup log destination: stdout or user specified file.
	var flog *os.File
	if logfile == "" {
		flog = os.Stdout
	} else {

		var err error

		flog, err = os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			log.Printf("%s\r\n", Error.E(op, err, errCantOpenFileForReading, 0, logfile))
			os.Exit(1)
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
			log.Printf("%s\r\n", err)
			os.Exit(1)
			return
		}

		if *savepassword {
			// user wants to save and exit
			gosavepassword(loginsSt, where.Username, *forHttps)
			return
		}

		loginFromFile, _, err := loginsSt.Find(where.Username, false)
		if err != nil {
			log.Printf("Login '%s' is not found in logins file %s\r\n", where.Username, passwordfile)
			os.Exit(1)
			return
		}

		// extracts hash of a password
		passwordhashBytes := make([]byte, hex.DecodedLen(len(loginFromFile.Passwordhash)))

		// transforms hash from ascii representation into bytes
		hex.Decode(passwordhashBytes, []byte(loginFromFile.Passwordhash))

		// decrypts hash bytes by using a Windows DPAPI for current windows user
		decryptedPasswordHash, err := dpapi.Decrypt(passwordhashBytes)
		if err != nil {
			log.Printf("Password decryption from DPAPI failed: %s\r\n", err)
			os.Exit(1)
			return
		}

		// holds hash in memory
		where.PasswordHash = string(decryptedPasswordHash)

	} /* else there is no password specified*/

	serviceUrl, err := url.ParseRequestURI(where.ToURL)

	if err != nil {
		// is needed to test an error in where.ToURL early. next use of Scheme is a worker()
		log.Printf("A 'service' parameter is bad: %s\r\n", err)
		os.Exit(1)
		return
	}

	requireCAcert := false
	// CA == certification authority that signed the service's certificate.
	if serviceUrl.Scheme == "https" {
		// add username to URL for HTTPS
		if where.ToURL[len(where.ToURL)-1] != '/' {
			where.ToURL += "/"
		}
		if !strings.HasSuffix(where.ToURL, "upload/") {
			log.Printf("You must add /upload/ to the end of the URL in the -service parameter.\r\n")
			log.Printf("Example: %v\r\n", exampleRun)
			os.Exit(1)
			return
		}
		where.ToURL += *username

		requireCAcert = true
		if *paramCAcert == "" {
			log.Printf("You must use 'cacert' parameter and specify a file with CA certificate in PEM format.\r\nYour service is %s\r\n", serviceUrl.Scheme)
			os.Exit(1)
			return
		}
	}

	// load CA certificate that signed the service's certificate.
	certpool := x509.NewCertPool()
	err = loadPemFileIntoCertPool(certpool, cacert)
	if err != nil && requireCAcert {
		log.Printf("A file with CA certificate must exist %s: %s\r\n", cacert, err)
		os.Exit(1)
		return
	}

	// TODO(zavla): load user certificate. For the user to authanticate to the service.
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

	if mainCtx.Err() == nil {

		log.Println("Normal exit.")
	}
}

// runWorkers starts payload goroutines.
// Every gorouting is then allowed to cancel the whole request after encouning an athorization error.
func runWorkers(oneForAllCtx context.Context, callmeToCancel context.CancelFunc, chNames chan string) {
	var wg sync.WaitGroup
	be1 := true
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go worker(oneForAllCtx, callmeToCancel, &wg, chNames)

		// Do not just run several goroutines - because tha user might specify wrong password.
		// Why start many goroutines with wrong password each?
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
			log.Printf("The goroutine has received a Cancel\r\n")
			return
		default:
		}
		err := prepareAndSendAFile(oneForAllCtx, name, &where)
		if errError, ok := err.(*Error.Error); ok && errError.Code == uploadclient.ErrAuthorizationFailed {
			callmeToCancel() // cancels the whole context and other goroutines
			log.Printf("cancelling the whole request because the service responded 'Authorization failed'\r\n")

			return
		}
	}

}

// prepareAndSendAFile works in a worker goroutine.
// It compute SHA1 of a file and calls uploadclient.SendAFile().
// At the end it calls markFileAsUploaded().
func prepareAndSendAFile(ctx context.Context, filename string, config *uploadclient.ConnectConfig) error {
	const op = "uploader.prepareAndSendAFile()"
	// uses cookies to hold sessionId
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // never error

	fullfilename, _ := filepath.Abs(filename)
	storagepath := filepath.Dir(fullfilename)
	name := filepath.Base(fullfilename)

	//compute SHA1 of a file
	bsha1, err := fsdriver.GetFileSha1(storagepath, name)
	if err != nil {
		log.Printf("%s\r\n", Error.E(op, err, errCantOpenFileForReading, 0, ""))
		return err
	}

	// figure out the scheme: https, ftp
	serviceURL, _ := url.ParseRequestURI(config.ToURL) // ignore error because we test in early in main()
	if serviceURL.Scheme == "ftp" || serviceURL.Scheme == "ftps" {
		err = uploadclient.FtpAFile(ctx, config, fullfilename, bsha1)
	} else {
		err = uploadclient.SendAFile(ctx, config, fullfilename, jar, bsha1)
	}

	if err == nil {
		log.Printf("Upload OK: %s\r\n", fullfilename)

		if !config.DontUseFileAttribute {
			if err := markFileAsUploaded(fullfilename); err != nil {
				// a non critical error
				log.Printf("%s\r\n", Error.E(op, err, errMarkFileFailed, 0, ""))
			}
		}
		// SUCCESS
		return nil
	}

	log.Printf("Error sending %s : %s\r\n", fullfilename, err)
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
			//println("skipping ", path)
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
		log.Printf("%s\r\n", Error.E(op, err, errReadingDirectory, 0, ""))
	}

	return
}

// savepasswordWithDPAPI asks for password, uses DPAPI to encrypt it, uses logins.Manager.Save() to store it.
// If usedInHTTPDigest == true additinally transforms password into HTTP digest hash form: username,realm, password.
func savepasswordWithDPAPI(loginsmanager logins.Manager, username string, usedInHTTPDigest bool, realm string) {
	const op = "uploader.savepasswordWithDPAPI"
	password, err := logins.AskPassword(username)
	if err != nil {
		log.Printf("Asking password failed: %s\r\n", err)
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
		log.Printf("Saving password file failed: %s\r\n", err)
		return
	}

	log.Printf("Password saved.\r\n")
	return

}
