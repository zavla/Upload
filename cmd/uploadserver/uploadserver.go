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

	Error "github.com/zavla/upload/errstr"
	"github.com/zavla/upload/httpDigestAuthentication"
	"github.com/zavla/upload/logins"
	"github.com/zavla/upload/uploadserver"

	//"encoding/base64"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

var gitCommit string

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
	paramStorageroot := flag.String("root", "", "storage root `path` for files.")
	flag.StringVar(&bindToAddress, "listenOn", "127.0.0.1:64000", "listen on specified `address:port`.")
	flag.StringVar(&bindToAddress2, "listenOn2", "", "listen on specified `address:port`.")
	paramConfigdir := flag.String("config", "", "`directory` with logins.json file (required).")
	flag.BoolVar(&asService, "asService", false, "start as a Windows service.")
	adduser := flag.String("adduser", "", "will add a login and save a password to logins.json file in -config dir.")
	paramAllowAnonymous := false //flag.Bool("allowAnonymous", false, "`true/false` to allow anonymous uploads.")
	paramVersion := flag.Bool("version", false, "print `version`")
	paramUsepprof := flag.Bool("debug", false, "debug, make available /debug/pprof/* URLs in service for profile")

	flag.Parse()
	flag.CommandLine.SetOutput(os.Stdout)
	flag.Usage = usage

	if *paramVersion {
		fmt.Printf("version: %s", gitCommit)
		os.Exit(0)
	}
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
		flag.Usage()
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
		flag.Usage()
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
	uploadserver.ConfigThisService.AllowAnonymousUse = paramAllowAnonymous
	uploadserver.ConfigThisService.Usepprof = *paramUsepprof

	if asService {
		// runsAsService is unique for windows and linux.
		// It responds to Windows Service Control Manager on windows.
		runsAsService(uploadserver.ConfigThisService)
	} else {

		config := uploadserver.ConfigThisService
		config.InitInterfacesConfigs()
		// err := config.UpdateInterfacesConfigs("") // here config.ifCongigs is created
		// if err != nil {
		// 	log.Printf("%s failed to create interfaces configuration, %s", op, err)

		// 	return
		// }
		// err = config.UpdateMapOfLogins()
		// if err != nil {
		// 	log.Printf("%s failed to load logins from logins.json, %s", op, err)

		// 	return
		// }

		endlessRunHTTPserver(&config)

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

// fnginLogFormater is almost a copy of gin.defaultLogFormatter
func fnginLogFormater(param gin.LogFormatterParams) string {
	if param.Latency > time.Minute {
		// Truncate in a golang < 1.8 safe way
		param.Latency = param.Latency - param.Latency%time.Second
	}
	paramPathUnEsc, err := url.PathUnescape(param.Path)
	if err != nil {
		paramPathUnEsc = param.Path
	}
	return fmt.Sprintf("[GIN] %v| %3d| %11v| %13s|%4s| %s| %s\n",
		param.TimeStamp.Format("2006/01/02 15:04:05"),
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
		paramPathUnEsc,
		param.ErrorMessage,
	)

}

// createOneHTTPHandler initialises from uploadserver.Config a new HTTP Handler, which is gin.Engine.
func createOneHTTPHandler(config *uploadserver.Config) *gin.Engine {
	// gin settings
	router := gin.New()
	slSkipov := []string{
		"/icons/back.gif",
		"/icons/blank.gif",
		"/icons/hand.right.gif",
		"/icons/unknown.gif",
		"/favicon.ico",
	}
	ginLoggerConfig := gin.LoggerConfig{Formatter: fnginLogFormater, Output: config.Logwriter, SkipPaths: slSkipov}
	router.Use(gin.LoggerWithConfig(ginLoggerConfig), gin.RecoveryWithWriter(config.Logwriter))

	// An authorization middleware. Gin executes this func for evey request.
	router.Use(func(c *gin.Context) {
		const op = "Login required."
		if strings.HasPrefix(c.Request.RequestURI, "/icons") ||
			strings.HasPrefix(c.Request.RequestURI, "/debug") {
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

	// no anonymous upload
	// router.Handle("GET", "/upload", uploadserver.ServeAnUpload)
	// router.Handle("POST", "/upload", uploadserver.ServeAnUpload)
	// per user upload
	router.Handle("GET", "/icons/*path", func(c *gin.Context) {

		p := config.RunningFromDir + "/htmltemplates/icons"
		iserve := http.StripPrefix("/icons", http.FileServer(http.Dir(p)))

		iserve.ServeHTTP(c.Writer, c.Request)
	})
	router.Handle("GET", "/upload/:login/*path", uploadserver.GetFileList)
	router.Handle("GET", "/upload/:login", uploadserver.GetFileList)

	router.Handle("POST", "/upload/:login", uploadserver.ServeAnUpload)

	if config.Usepprof {

		router.Handle("GET", "/debug/pprof", func(c *gin.Context) {
			pprof.Index(c.Writer, c.Request)
		})
		router.Handle("GET", "/debug/profile", func(c *gin.Context) {
			pprof.Profile(c.Writer, c.Request)
		})
	}
	return router
}

// endlessRunHTTPserver starts goroutines for endlessly running runHTTPserver() on every interface from config.
// Every goroutine waits for different resources being attached: disk, ip interface.
func endlessRunHTTPserver(config *uploadserver.Config) {

	for {
		err := config.UpdateMapOfLogins()
		// logins.json must exist
		if err == nil {
			break
		}
		log.Printf("service is waiting for the config dir to become available to read logins.json\n")
		time.Sleep(20 * time.Second)
	}
	log.Printf("service has read the logins.json file\n")
	handler := createOneHTTPHandler(config)

	//func
	forOneInterface := func(netinterface string) {
		log.Printf("trying service on net interface %s\n", netinterface)
		for {
			err := config.UpdateInterfacesConfigs(netinterface)
			if err != nil {

				pemfilename := config.FilenamefromNetInterface(netinterface)
				log.Printf("service didn't found files with certificates: %s.pem, %s-key.pem at %s\n", pemfilename, pemfilename, config.Configdir)
				time.Sleep(20 * time.Second)

				continue
			}

			wa := &sync.WaitGroup{}
			wa.Add(1)
			go runHTTPserver(wa, handler, config, netinterface) // may fail if there is no disk or net interface.
			// we wait and restart one more time
			wa.Wait()
			const period20sec = 20
			log.Printf("service is waiting %d sec to restart HTTP server on interface %s\n", period20sec, netinterface)
			time.Sleep(period20sec * time.Second)
		}
	}
	for _, v := range config.IfConfigs {
		go forOneInterface(v.Listenon) //uses common handler and config, but different interface/address.
	}
}

// runHTTPserver runs http.Server.ListenAndServe on one interface with a specified http.Handler.
func runHTTPserver(wa *sync.WaitGroup, handler http.Handler, config *uploadserver.Config, listenon string) {
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

	log.Printf("Service is listening on %s now\n", interfaceConfig.Listenon)
	err := s.ListenAndServeTLS(interfaceConfig.CertFile, interfaceConfig.KeyFile)
	if err != http.ErrServerClosed { // expects error
		log.Println(Error.E(op, err, errServiceExitedAbnormally, 0, ""))
	}

}
func usage() {
	fmt.Println(`Copyright zakhar.malinovskiy@gmail.com, `, gitCommit)
	fmt.Printf(`Usage: 
uploadserver -root dir [-log file] -config dir -listenOn ip:port [-listenOn2 ip:port] [-debug] [-asService]
uploadserver -adduser name -config dir

`)

}
