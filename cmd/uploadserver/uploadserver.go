// For Windows you may run upload service as a Windows service.
// To create a Windows service run:
// New-Service -Name upload -BinaryPathName f:\Zavla_VB\GO\src\upload\cmd\uploadserver\uploadserver.exe  -Description "holds your backups" -StartupType Manual -asService

package main

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"os/signal"
	"strings"
	"sync"
	Error "upload/errstr"
	"upload/httpDigestAuthentication"
	"upload/logins"
	"upload/uploadserver"

	//"encoding/base64"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {

	const op = "uploadserver.main()"
	const constRealm = "upload" // this is for http digest authantication predefined realm,
	// It is used to store passwords hashes in a file.

	var ( // command line flags
		bindToAddress  string
		bindToAddress2 string
		storageroot    string
		configdir      string
		asService      bool
		logname        string
	)
	paramLogname := flag.String("log", "", "log `file` name.")
	paramStorageroot := flag.String("root", "", "storage root `path` for files (required).")
	flag.StringVar(&bindToAddress, "listenOn", "127.0.0.1:64000", "listens on specified `address:port`.")
	flag.StringVar(&bindToAddress2, "listenOn2", "", "listens on specified `address:port`.")
	paramConfigdir := flag.String("config", "", "`directory` with logins.json file (required).")
	flag.BoolVar(&asService, "asService", false, "start as a service (windows services or linux daemon).")
	adduser := flag.String("adduser", "", "will add a `login` and save a password to a file specified with passwordfile.")
	paramAllowAnonymous := flag.Bool("allowAnonymous", false, "`true/false` to allow anonymous uploads.")

	flag.Parse()
	configdir, _ = filepath.Abs(*paramConfigdir)

	// setup log destination
	var logwriter io.Writer // io.MultiWriter
	var logfile *os.File

	// uses log because log.out uses mutex
	log.SetPrefix("[UPL] ")
	log.SetFlags(log.LstdFlags)

	if *paramLogname == "" {
		logwriter = os.Stdout
	} else {
		logname, _ = filepath.Abs(*paramLogname)
		var err error
		logfile, err = os.OpenFile(logname, os.O_APPEND|os.O_CREATE|os.O_RDWR, os.ModeAppend)
		if err != nil { // do not start without log file
			log.Fatal(Error.E(op, err, errCantWriteLogFile, 0, logname))
		}
		logwriter = io.MultiWriter(logfile, os.Stdout)

	}
	log.SetOutput(logwriter)
	defer logfile.Close()

	// here we have a working log file
	if configdir == "" {
		flag.PrintDefaults()
		log.Fatalf("-configdir is required.")
		return
	}

	if *adduser != "" {
		loginsSt := logins.Logins{}

		loginsfilename := filepath.Join(configdir, "logins.json")
		err := loginsSt.OpenDB(loginsfilename)
		if err != nil {
			log.Printf("Can't open logins.json file : %s\n", err)
			return
		}
		loginobj := logins.Login{Login: *adduser}

		err = logins.AskAndSavePasswordForHTTPDigest(&loginsSt, loginobj, constRealm)
		if err != nil {
			log.Printf("Can't write logins.json file : %s\n", err)
			return
		}
		log.Printf("Password for login '%s' saved to %s\n", *adduser, loginsfilename)

		return
	}

	// check required params
	if *paramStorageroot == "" {
		flag.PrintDefaults()
		return
	}
	storageroot, err := filepath.Abs(*paramStorageroot)
	if err != nil {
		log.Printf("Can't get absolute path of storageroot: %s\n", err)
		return
	}

	froot, err := openStoragerootRw(storageroot)
	if err != nil {
		log.Printf("Can't start server, storageroot rw error: %s\n", err)

		// let the service start, it will wait for the storageroot attachment
	} else {

		defer froot.Close()
	}
	sladdr := make([]string, 0, 2) //cap==2
	if bindToAddress != "" {
		sladdr = append(sladdr, bindToAddress)
	}
	if bindToAddress2 != "" {
		sladdr = append(sladdr, bindToAddress2)
	}
	uploadserver.ConfigThisService.BindAddress = sladdr      // creates a slice of listenon addresses
	uploadserver.ConfigThisService.Storageroot = storageroot // the root directory

	// where we started from?
	rundir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Printf("Can't find starting directory of server executable (used readonly). %s\n", err)
		return
	}
	uploadserver.ConfigThisService.RunningFromDir = rundir
	uploadserver.ConfigThisService.Configdir = configdir
	uploadserver.ConfigThisService.Logwriter = logwriter
	uploadserver.ConfigThisService.AllowAnonymousUse = *paramAllowAnonymous

	if asService {
		// runsAsService is unique for windows and linux.
		// It responds to Windows Service Control Manager on windows.
		runsAsService(uploadserver.ConfigThisService)
	} else {

		config := uploadserver.ConfigThisService
		err := config.UpdateInterfacesConfigs()
		if err != nil {
			log.Printf("%s failed to create interfaces configuration, %s", op, err)

			return
		}
		err = config.UpdateMapOfLogins()
		if err != nil {
			log.Printf("%s failed to load logins from logins.json, %s", op, err)

			return
		}

		endlessRunHTTPserver(config)

		chwithSignal := make(chan os.Signal, 1)
		signal.Notify(chwithSignal, os.Interrupt)
		s := <-chwithSignal
		log.Printf("os.Interrupt %s recieved.", s)

	}

	log.Println("uploadserver main() exited.")

}

func openStoragerootRw(storageroot string) (*os.File, error) {
	mkerr := os.MkdirAll(storageroot, 0400)
	if mkerr != nil {
		return nil, mkerr
	}
	d, err := os.OpenFile(storageroot, os.O_RDONLY, 0400)

	if err != nil {
		return nil, err
	}

	return d, nil
}

// loginCheck implements HTTP digest authorization scheme with additional header from server
// that proves the server has the right password hash.
// Clients may check this additional header to destinguish fake servers.
func loginCheck(c *gin.Context, loginsmap map[string]logins.Login) {
	const op = "cmd/uploadserver.loginCheck()"
	h := md5.New()
	yyyy, mm, dd := time.Now().Date()
	io.WriteString(h, fmt.Sprintf("%d:%d:%d", yyyy, mm, dd))
	currentNonce := hex.EncodeToString(h.Sum(nil))

	challenge := httpDigestAuthentication.ChallengeToClient{
		Realm:     "upload",
		Domain:    "upload.com",
		Nonce:     currentNonce,
		Opaque:    "",
		Stale:     "",
		Algorithm: "MD5",
		Qop:       "auth",
	}
	authorization := c.GetHeader("Authorization")
	if authorization == "" {
		wwwauthenticate := httpDigestAuthentication.GenerateWWWAuthenticate(&challenge)
		c.Header("WWW-Authenticate", wwwauthenticate)
		c.AbortWithStatus(http.StatusUnauthorized)
		//c.Error(Error.E(op, nil, errNoError, Error.ErrKindInfoForUsers, "Header 'authorization' is empty."))
		return
	}
	// here we have "Authorization" from client with some text.
	creds, err := httpDigestAuthentication.ParseStringIntoStruct(authorization)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		c.Error(Error.E(op, err, uploadserver.ErrAuthorizationFailed, 0, authorization))

		return
	}
	creds.Method = c.Request.Method // client uses its Method in its hash, acording to specification

	currlogin := logins.Login{}
	currlogin, ok := loginsmap[creds.Username]
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		c.Error(Error.E(op, err, uploadserver.ErrAuthorizationFailed, 0, fmt.Sprintf("username not found: %s", creds.Username)))

		return
	}
	access, err := httpDigestAuthentication.CheckCredentialsFromClient(&challenge, creds, currlogin.Passwordhash)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		c.Error(Error.E(op, err, uploadserver.ErrAuthorizationFailed, 0, fmt.Sprintf("HTTP digest authorization method failed for user: %s", creds.Username)))

		return
	}
	if !access {
		c.AbortWithStatus(http.StatusUnauthorized)
		c.Error(Error.E(op, err, uploadserver.ErrAuthorizationFailed, 0, fmt.Sprintf("password failed for user: %s", creds.Username)))
		return
	}
	//granted

	// server proves it has write password.
	// TODO(zavla): prove on every client's request???
	responsewant, _ := httpDigestAuthentication.GenerateResponseAuthorizationParameter(currlogin.Passwordhash, creds) // no errror, otherwise we can't be here
	c.Header(httpDigestAuthentication.KeyProvePeerHasRightPasswordhash,
		httpDigestAuthentication.ProveThatPeerHasRightPasswordhash(currlogin.Passwordhash, responsewant))

	c.Set(gin.AuthUserKey, creds.Username) // grants a login
	return                                 // normal exits and calls other handlers
}

// createOneHTTPHandler initialises from uploadserver.Config a new HTTP Handler, which is gin.Engine.
func createOneHTTPHandler(config uploadserver.Config) *gin.Engine {
	// gin settings
	router := gin.New()

	// TODO(zavla): seems like gin.LoggerWithWriter do not protect its Write() to log file with mutex
	router.Use(gin.LoggerWithWriter(config.Logwriter,
		"/icons/back.gif",
		"/icons/blank.gif",
		"/icons/hand.right.gif",
		"/icons/unknown.gif",
		"/favicon.ico",
	),
		gin.RecoveryWithWriter(config.Logwriter),
	)

	// An authorization middleware. Gin executes this func for evey request.
	router.Use(func(c *gin.Context) {
		const op = "Login required."
		if strings.HasPrefix(c.Request.RequestURI, "/icons") {
			c.Next()
			return
		}
		loginFromURL := c.Param("login")
		if loginFromURL == "" && !config.AllowAnonymousUse {
			c.AbortWithStatusJSON(http.StatusNotAcceptable, gin.H{"error": Error.ToUser(op, uploadserver.ErrAuthorizationFailed, "Service supports HTTP digest method. Specify a user name as a URL path. .../upload/usernamehere").Error()})

			return
		}
		if loginFromURL != "" /*&& c.Request.Method != "GET"*/ {
			// authorization.
			loginCheck(c, config.LoginsMap)
		}
		c.Next()
	})

	// anonymous upload
	router.Handle("GET", "/upload", uploadserver.ServeAnUpload)
	router.Handle("POST", "/upload", uploadserver.ServeAnUpload)
	// per user upload
	router.Handle("GET", "/icons/*path", func(c *gin.Context) {
		p := `f:\Zavla_VB\go\src\upload\cmd\uploadserver\htmltemplates\icons\`
		//config.RunningFromDir + "/htmltemplates/icons"
		iserve := http.StripPrefix("/icons", http.FileServer(http.Dir(p)))

		iserve.ServeHTTP(c.Writer, c.Request)
	})
	router.Handle("GET", "/upload/:login/*path", uploadserver.GetFileList)
	router.Handle("GET", "/upload/:login", uploadserver.GetFileList)

	router.Handle("POST", "/upload/:login", uploadserver.ServeAnUpload)
	return router
}

// endlessRunHTTPserver starts goroutines for endlessly running runHTTPserver() on every interface from config.
// Every goroutine waits for different resources being attached: disk, ip interface.
func endlessRunHTTPserver(config uploadserver.Config) {

	handler := createOneHTTPHandler(config)
	//func
	forOneInterface := func(netinterface string) {
		for {
			wa := &sync.WaitGroup{}
			wa.Add(1)
			go runHTTPserver(wa, handler, config, netinterface) // may fail if there is no disk or net interface.
			// we wait and restart one more time
			wa.Wait()
			log.Printf("service is waiting 10sec to restart HTTP server on interface %s\n", netinterface)
			time.Sleep(20 * time.Second)
		}
	}
	for _, v := range config.IfConfigs {
		go forOneInterface(v.Listenon) //uses common handler and config, but different interface/address.
	}
}

// runHTTPserver runs http.Server.ListenAndServe on one interface with a specified http.Handler.
func runHTTPserver(wa *sync.WaitGroup, handler http.Handler, config uploadserver.Config, listenon string) {
	const op = "cmd/uploadserver.runHTTPserver()"
	defer wa.Done() // after exit WorkGroup will be done.

	var tlsConfig *tls.Config //not used so far

	interfaceConfig := config.IfConfigs[listenon] // here we specified certificates files names.

	s := &http.Server{
		Addr:      interfaceConfig.Listenon,
		Handler:   handler,
		TLSConfig: tlsConfig,
		// 8 hours for big uploads, clients should retry after that.
		// Anonymous uploads have no means to retry uploads.
		ReadTimeout: 8 * 3600 * time.Second, // is an ENTIRE time on reading the request including reading the request body
		// WriteTimeout:      60 * time.Second,  // total time to write the response, after this time the server will close a connection (but will complete serving current request with a response 0 body length send).
		WriteTimeout:      12 * 3600 * time.Second, // if server didn't manage to write response (it was busy writing a file) within this time - server closes connection.
		ReadHeaderTimeout: 10 * time.Second,        // we need relatively fast response on inaccessable client
		IdleTimeout:       120 * time.Second,       // time for client to post a second request.
		//MaxHeaderBytes: 1000,
	}

	log.Printf("Service is starting on %s\n", interfaceConfig.Listenon)
	err := s.ListenAndServeTLS(interfaceConfig.CertFile, interfaceConfig.KeyFile)
	if err != http.ErrServerClosed { // expects error
		log.Println(Error.E(op, err, errServiceExitedAbnormally, 0, ""))
	}

}
