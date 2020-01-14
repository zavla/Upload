package main

import (
	Error "Upload/errstr"
	"Upload/httpDigestAuthentication"
	"Upload/logins"
	"Upload/uploadserver"
	"crypto/md5"
	"encoding/hex"
	"fmt"

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

var bindToAddress string

//const bindToAddress = "1cprogrammer:8888"

var (
//errCantWriteLogFile = Error.E("uploadservermain", nil, Error.ErrFileIO, "Can not start. Can't write to a log file.")
)

// server stores all the files here in sub dirs
var storageroot string
var configdir string

func main() {
	const op = "uploadserver.main()"
	logname := flag.String("log", "", "log file path and name.")
	flag.StringVar(&storageroot, "storageroot", "", "storage root path for files")
	flag.StringVar(&bindToAddress, "listenOn", "127.0.0.1:64000", "listens on specified address:port")
	flag.StringVar(&configdir, "configdir", "", "directory with configuration files")

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
		log.Printf("--storageroot required")
		flag.PrintDefaults()
		return
	}
	storageroot, err := filepath.Abs(storageroot)
	if err != nil{
		log.Printf("Can't get absolute path of storageroot: %s", err)
		return
	}
	froot, err := openStoragerootRw(storageroot)
	if err != nil {
		log.Printf("Can't start server, storageroot rw error: %s", err)
		return
	}
	defer froot.Close()

	uploadserver.Storageroot = storageroot

	// where we started from?
	rundir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Printf("Can't find starting directory of server executable (used readonly). %s", err)
		return
	}
	uploadserver.RunningFromDir = rundir
	// reads logins passwords
	loginsstruct, err := logins.ReadLoginsJSON(filepath.Join(configdir, "logins.file"))
	loginsMap := getMapOfLogins(loginsstruct)

	router := gin.New()
	// TODO(zavla): seems like gin.LoggerWithWriter do not protect its Write() to log file with mutex
	router.Use(gin.LoggerWithWriter(logwriter),
		gin.RecoveryWithWriter(logwriter))
	// auth middleware. Gin executes a func for evey request.
	router.Use(func(c *gin.Context) {
		loginFromUrl := c.Param("login")
		if loginFromUrl != "" {
			//gin.BasicAuthForRealm(loginsMap, "upload")(c)
			loginCheck(c, loginsMap)
		}
		c.Next()
		// otherwise no authorization
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
		Addr:              bindToAddress,
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
	log.Println("Uploadserver main() exited.")

}

func getMapOfLogins(loginsstruct logins.Logins) map[string]logins.Login {
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
		// TODO: need log
		return
	}
	creds.Method = c.Request.Method // client used its method in hash

	currlogin := logins.Login{}
	currlogin, ok := loginsmap[creds.Username]
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		// TODO: need log
		log.Printf("Username not found: %s",creds.Username)
		return
	}
	access, err := httpDigestAuthentication.CheckCredentialsFromClient(&challenge, creds, currlogin.Passwordhash)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		log.Printf("Error while login in: %s",creds.Username)
		return
	}
	if !access {
		c.AbortWithStatus(http.StatusUnauthorized)
		log.Printf("Login failed: %s",creds.Username)
		return
	}
	//granted
	c.Set(gin.AuthUserKey, creds.Username) // grants a login
}
