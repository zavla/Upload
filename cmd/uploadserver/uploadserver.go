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
	"runtime"
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
		fmt.Printf("version: %s\r\n", gitCommit)
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
		logfile = os.Stdout
	} else {
		logname, _ = filepath.Abs(*paramLogname)
		var err error
		logfile, err = os.OpenFile(logname, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0660)
		if err != nil { // do not start without log file
			log.Fatal(Error.E(op, err, errCantWriteLogFile, 0, logname))
		}
		logwriter = io.MultiWriter(logfile, os.Stdout)

	}
	log.SetOutput(logwriter)
	defer logfile.Close()

	// Print stack on panic
	defer stackPrintOnPanic("main()")

	// here we have a working log file
	if configdir == "" {
		flag.Usage()
		flag.PrintDefaults()
		log.Fatalf("-configdir is required.\r\n")
		return
	}

	if *adduser != "" {
		loginsSt := logins.Logins{}

		loginsfilename := filepath.Join(configdir, "logins.json")
		err := loginsSt.OpenDB(loginsfilename)
		if err != nil {
			log.Printf("Can't open logins.json file : %s\r\n", err)
			return
		}
		loginobj := logins.Login{Login: *adduser}

		err = logins.AskAndSavePasswordForHTTPDigest(&loginsSt, loginobj, constRealm)
		if err != nil {
			log.Printf("Can't write logins.json file : %s\r\n", err)
			return
		}
		log.Printf("Password for login '%s' saved to %s\r\n", *adduser, loginsfilename)

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
		log.Printf("Can't get absolute path of directory 'storageroot': %s\r\n", err)
		return
	}

	froot, err := openStoragerootRw(storageroot)
	if err != nil {
		log.Printf("Can't start server, storageroot rw error: %s\r\n", err)

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
	uploadserver.ConfigThisService.Logfile = logfile
	uploadserver.ConfigThisService.BindAddress = sladdr      // creates a slice of listenon addresses
	uploadserver.ConfigThisService.Storageroot = storageroot // the root directory

	// where we started from?
	rundir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Printf("Can't find starting directory of server executable (readonly). %s\r\n", err)
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
		log.Printf("os.Interrupt %s recieved.\r\n", s)

	}

	log.Println("uploadserver main() exited.")

}

func stackPrintOnPanic(where string) {
	// on panic we will write to log file
	if err := recover(); err != nil {
		// no '\r' symbols in log line
		// TODO(zavla): make structured log file?
		log.Printf("PANIC: in %s\n, error: %s\n", where, err)
		b := make([]byte, 2500) // enough buffer for stack trace text
		n := runtime.Stack(b, true)
		b = b[:n]
		// output stack trace to a log
		log.Printf("%d bytes of stack trace\n%s\nENDPANIC\r\n", n, string(b))
	}
}

func openStoragerootRw(storageroot string) (*os.File, error) {
	mkerr := os.MkdirAll(storageroot, 0660)
	if mkerr != nil {
		return nil, mkerr
	}
	d, err := os.OpenFile(storageroot, os.O_RDONLY, 0660)

	if err != nil {
		return nil, err
	}

	return d, nil
}

// loginCheck implements HTTP digest authorization scheme with additional header from server
// that proves that the server has the right password hash of a user password.
// Clients may check this additional header to distinguish fake servers.
func loginCheck(c *gin.Context, username string, loginsmap map[string]logins.Login) {
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
		return
	}
	// here we have "Authorization" from client with some text.
	creds, err := httpDigestAuthentication.ParseStringIntoStruct(authorization)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		c.Error(Error.E(op, err, uploadserver.ErrAuthorizationFailed, 0, authorization))

		return
	}

	// we use login name from the URL and username from HTTP authentication
	// don't allow stale HTTP authorization to access another user's profile
	if username != creds.Username {
		wwwauthenticate := httpDigestAuthentication.GenerateWWWAuthenticate(&challenge)
		c.Header("WWW-Authenticate", wwwauthenticate)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	creds.Method = c.Request.Method // client uses its Method in its hash, according to specification

	currlogin := logins.Login{}
	currlogin, ok := loginsmap[creds.Username]
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Username not fount"})
		c.Abort()
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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "password failed"})
		c.Abort()
		c.Error(Error.E(op, err, uploadserver.ErrAuthorizationFailed, 0, fmt.Sprintf("password failed for user: %s", creds.Username)))
		return
	}
	//granted

	// server proves it has write password.
	// TODO(zavla): prove on every client's request???
	responsewant, _ := httpDigestAuthentication.GenerateResponseAuthorizationParameter(currlogin.Passwordhash, creds)
	// no error, otherwise we can't be here
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
	return fmt.Sprintf("[UPL] %v| %3d| %11v| %13s|%4s| %s| %s\n",
		param.TimeStamp.Format("2006/01/02 15:04:05"),
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
		paramPathUnEsc,
		param.ErrorMessage,
	)

}

// createOneHTTPHandler initializes from uploadserver.Config a new HTTP Handler, which is gin.Engine.
func createOneHTTPHandler(config *uploadserver.Config) *gin.Engine {
	// gin settings
	router := gin.New()

	// accesse wil not be logged by gin
	skipPaths := []string{
		"/icons/back.gif",
		"/icons/blank.gif",
		"/icons/hand.right.gif",
		"/icons/unknown.gif",
		"/favicon.ico",
	}
	ginLoggerConfig := gin.LoggerConfig{
		Formatter: fnginLogFormater,
		Output:    config.Logwriter,
		SkipPaths: skipPaths,
	}
	router.Use(gin.LoggerWithConfig(ginLoggerConfig), gin.RecoveryWithWriter(config.Logwriter))

	// An authorization middleware. Gin executes this func for every request.
	router.Use(func(c *gin.Context) {
		const op = "Login required."
		if strings.HasPrefix(c.Request.RequestURI, "/icons") ||
			c.Request.RequestURI == "/favicon.ico" {
			c.Next() // no login check
			return   // no login check
		}
		loginFromURL := c.Param("login")
		if loginFromURL != "" ||
			// /debug/pprof is a fixed prefig from package net/http/pprof
			strings.HasPrefix(c.Request.RequestURI, "/debug/pprof") ||
			strings.HasPrefix(c.Request.RequestURI, "/log") {
			// authorization.
			username := loginFromURL
			if username == "" {
				username = "debug"
				// builtin debug user, to disable it create a user 'debug' with your password
				if _, ok := config.LoginsMap[username]; !ok {
					config.LoginsMap[username] = logins.Login{
						Login:        username,
						Passwordhash: "756b1782d6ddbaacc1b72f68d7428485",
					}
				}
			}
			loginCheck(c, username, config.LoginsMap)

			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusNotAcceptable,
			gin.H{
				"error": Error.ToUser(op, uploadserver.ErrAuthorizationFailed, "Service supports HTTP digest method. Specify a user name as a URL path, ex. https://127.0.0.1:64000/upload/usernamehere").Error(),
			},
		)
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
	router.Handle("GET", "/log", uploadserver.GetLogContent)
	router.Handle("GET", "/upload/:login/*path", uploadserver.GetFileList)
	router.Handle("GET", "/upload/:login", uploadserver.GetFileList)

	router.Handle("POST", "/upload/:login", uploadserver.ServeAnUpload)

	if config.Usepprof {

		router.Handle("GET", "/debug/pprof/*profiletype", func(c *gin.Context) {
			pprof.Index(c.Writer, c.Request)
		})
		// router.Handle("GET", "/debug/:login/profile", func(c *gin.Context) {
		// 	pprof.Profile(c.Writer, c.Request)
		// })
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
		log.Printf("service is waiting for the config directory to become available to read file logins.json\r\n")
		time.Sleep(20 * time.Second)
	}
	log.Printf("service has read the logins.json file\r\n")

	// create a gin.Engine
	handler := createOneHTTPHandler(config)

	//func
	forOneInterface := func(netinterface string) {
		// function runs in gorouting and may panic somehow
		const op = "cmd/uploadserver.forOneInterface()"
		defer stackPrintOnPanic(op)
		log.Printf("trying service on net interface %s\r\n", netinterface)
		for {
			err := config.UpdateInterfacesConfigs(netinterface)
			if err != nil {

				pemfilename := config.FilenamefromNetInterface(netinterface)
				log.Printf("service didn't found files with certificates: %s.pem, %s-key.pem at %s\r\n", pemfilename, pemfilename, config.Configdir)
				time.Sleep(20 * time.Second)

				continue
			}

			wa := &sync.WaitGroup{}
			wa.Add(1)
			go runHTTPserver(wa, handler, config, netinterface) // may fail if there is no disk or net interface.
			// we wait for http server to return and restart it more time
			wa.Wait()
			const period20sec = 20
			log.Printf("service is waiting %d sec to restart HTTP server on interface %s\r\n", period20sec, netinterface)
			time.Sleep(period20sec * time.Second)
		}
	}
	for _, v := range config.IfConfigs {
		// parallel http servers for every interface
		go forOneInterface(v.Listenon) //uses common handler and config, but different interface/address.
	}
}

// runHTTPserver runs http.Server.ListenAndServe on one interface with a specified http.Handler.
func runHTTPserver(wa *sync.WaitGroup, handler http.Handler, config *uploadserver.Config, listenon string) {
	const op = "cmd/uploadserver.runHTTPserver()"
	defer wa.Done() // after exit WorkGroup will be done.
	defer stackPrintOnPanic(op)

	var tlsConfig *tls.Config //not used so far

	// here we specified certificates files names.
	interfaceConfig := config.IfConfigs[listenon]

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

	log.Printf("service is going to listen on %s now\r\n", interfaceConfig.Listenon)
	err := s.ListenAndServeTLS(interfaceConfig.CertFile, interfaceConfig.KeyFile)
	if err != http.ErrServerClosed { // expects this error
		// other errors go to log
		log.Println(Error.E(op, err, errServiceExitedAbnormally, 0, ""))
	}

}
func usage() {
	fmt.Println(`Copyright zakhar.malinovskiy@gmail.com, `, gitCommit)
	fmt.Printf(`Usage: 
uploadserver -root dir [-log file] -config dir -listenOn ip:port [-listenOn2 ip:port] [-debug] [-asService]
or
uploadserver -adduser name -config dir

`)

}
