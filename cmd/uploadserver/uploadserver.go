package main

import (
	"Upload/errstr"
	"Upload/uploadserver"
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
	errCantWriteLogFile = *errstr.NewError("uploadservermain", 0, "Can not start. Can't write to a log file.")
)

// server stores all the files here in sub dirs
var storageroot string

func main() {
	logname := flag.String("log", "", "log file path and name.")
	flag.StringVar(&storageroot, "storageroot", "", "storage root path for files")
	flag.StringVar(&bindToAddress, "listenOn", "127.0.0.1:64000", "listens on specified address:port")

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
			log.Fatal(errCantWriteLogFile.SetDetails("filename: %s", *logname))
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
	storageroot = filepath.Clean(storageroot)
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
		log.Printf("Can't find starting directory of server executable (readonly). %s", err)
		return
	}
	uploadserver.RunningFromDir = rundir

	router := gin.New()
	// TODO(zavla): seems like gin.LoggerWithWriter do not protect its Write() to log file with mutex
	router.Use(gin.LoggerWithWriter(logwriter),
		gin.RecoveryWithWriter(logwriter))
	router.Handle("GET", "/upload", uploadserver.ServeAnUpload)
	router.Handle("POST", "/upload", uploadserver.ServeAnUpload)
	router.Handle("GET", "/list", uploadserver.GetFileList)

	//router.Run(bindToAddress)  timeouts needed
	s := &http.Server{
		Addr:              bindToAddress,
		Handler:           router,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      60 * time.Second,
		ReadHeaderTimeout: 60 * time.Second,
		//MaxHeaderBytes: 1000,
	}
	s.ListenAndServe()
	log.Println("Uploadserver main() exited.")

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
