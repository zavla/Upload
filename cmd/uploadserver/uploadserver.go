// For Windows you may run upload service as a Windows service.
// To create a Windows service run:
// New-Service -Name upload -BinaryPathName f:\Zavla_VB\GO\src\upload\cmd\uploadserver\uploadserver.exe  -Description "holds your backups" -StartupType Manual

package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
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
	var ( // command line flags
		bindToAddress string
		storageroot   string
		configdir     string
		asService     bool
	)
	const op = "uploadserver.main()"
	logname := flag.String("log", "", "log file name, relative to storage root.")
	flag.StringVar(&storageroot, "root", "", "storage root path for files (required)")
	flag.StringVar(&bindToAddress, "listenOn", "127.0.0.1:64000", "listens on specified address:port")
	flag.StringVar(&configdir, "config", "", "directory with logins.json file.")
	flag.BoolVar(&asService, "asService", false, "start as a service (windows services or linux daemon)")
	flag.Parse()

	// setup log destination
	var logwriter io.Writer // io.MultiWriter
	var logfile *os.File

	// uses log because log.out uses mutex
	log.SetPrefix("[LogMsg] ")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if *logname == "" {
		logwriter = os.Stdout
	} else {
		var err error
		logfile, err = os.OpenFile(*logname, os.O_APPEND|os.O_CREATE|os.O_RDWR, os.ModeAppend)
		if err != nil { // do not start without log file
			log.Fatal(Error.E(op, err, errCantWriteLogFile, 0, *logname))
		}
		logwriter = io.MultiWriter(logfile, os.Stdout)

	}
	log.SetOutput(logwriter)
	defer logfile.Close()

	// here we have a working log file

	// check required params
	if storageroot == "" {
		flag.PrintDefaults()
		return
	}
	storageroot, err := filepath.Abs(storageroot)
	if err != nil {
		log.Printf("Can't get absolute path of storageroot: %s", err)
		return
	}
	froot, err := openStoragerootRw(storageroot)
	if err != nil {
		log.Printf("Can't start server, storageroot rw error: %s", err)
		return
	}
	defer froot.Close()

	uploadserver.ConfigThisService.BindAddress = bindToAddress
	uploadserver.ConfigThisService.Storageroot = storageroot

	// where we started from?
	rundir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Printf("Can't find starting directory of server executable (used readonly). %s", err)
		return
	}
	uploadserver.ConfigThisService.RunningFromDir = rundir
	uploadserver.ConfigThisService.Configdir = configdir
	uploadserver.ConfigThisService.Logwriter = logwriter

	if asService {
		// runsAsService is unique for windows and linux.
		// It responds to Windows Service Control Manager on windows.
		runsAsService(uploadserver.ConfigThisService)
	} else {
		runHTTPserver(uploadserver.ConfigThisService)
	}

	log.Println("uploadserver main() exited.")

}

func loginsToMap(loginsstruct logins.Logins) map[string]logins.Login {
	ret := make(map[string]logins.Login)
	for _, l := range loginsstruct.Logins {
		ret[l.Login] = l
	}
	return ret
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
		return
	}
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
	c.Set(gin.AuthUserKey, creds.Username) // grants a login
}

func runHTTPserver(config uploadserver.Config) {
	const op = "cmd/uploadserver.runHTTPserver()"
	// reads logins passwords
	loginsstruct, err := logins.ReadLoginsJSON(filepath.Join(config.Configdir, "logins.json"))
	if config.Configdir != "" {
		if err != nil {
			// if configdir is specified , a file logins.json must exist
			log.Printf("If you specify a config directory, there must exist a logins.json file.")
			return
		}
	}
	loginsMap := loginsToMap(loginsstruct)

	router := gin.New()
	// TODO(zavla): seems like gin.LoggerWithWriter do not protect its Write() to log file with mutex
	router.Use(gin.LoggerWithWriter(config.Logwriter),
		gin.RecoveryWithWriter(config.Logwriter))

	// Our authantication middleware. Gin executes this func for evey request.
	router.Use(func(c *gin.Context) {
		loginFromURL := c.Param("login")
		if loginFromURL != "" {
			loginCheck(c, loginsMap)
		}
		c.Next()
		// otherwise no authorization. No login == anonymous access.
	})

	// anonymous upload
	router.Handle("GET", "/upload", uploadserver.ServeAnUpload)
	router.Handle("POST", "/upload", uploadserver.ServeAnUpload)
	// per user upload
	router.Handle("GET", "/upload/*login", uploadserver.ServeAnUpload)
	router.Handle("POST", "/upload/*login", uploadserver.ServeAnUpload)
	router.Handle("GET", "/list/*login", uploadserver.GetFileList)

	//router.Run(bindToAddress)  timeouts needed
	s := &http.Server{
		Addr:              config.BindAddress,
		Handler:           router,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      60 * time.Second,
		ReadHeaderTimeout: 60 * time.Second,
		//MaxHeaderBytes: 1000,
	}
	err = s.ListenAndServe()
	if err != http.ErrServerClosed { // expects error
		log.Println(Error.E(op, err, errServiceExitedAbnormally, 0, ""))
	}

}
