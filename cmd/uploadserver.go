package main

import (
	"Upload/errstr"
	"Upload/uploadserver"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

const bindToAddress = "127.0.0.1:64000"

var (
	errCantWriteLogFile = *errstr.NewError("uploadservermain", 0, "Can not start. Can't write to a log file.")
)

func main() {
	logname := flag.String("log", "", "log file path and name.")
	flag.Parse()
	// setup log destination
	var flog io.Writer // io.MultiWriter
	var flogfile *os.File

	// uses log because log.out uses mutex
	log.SetPrefix("[LogMsg] ")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if *logname == "" {
		flog = os.Stdout
	} else {
		var err error
		flogfile, err = os.OpenFile(*logname, os.O_APPEND|os.O_CREATE|os.O_RDWR, os.ModeAppend)
		if err != nil { // do not start without log file
			log.Fatal(errCantWriteLogFile.SetDetails("filename: %s", *logname))
		}
		flog = io.MultiWriter(flogfile, os.Stdout)
	}
	log.SetOutput(flog)
	defer flogfile.Close()

	router := gin.New()
	// TODO(zavla): seems like gin.LoggerWithWriter do not protect its Write() to log file with mutex
	router.Use(gin.LoggerWithWriter(flog),
		gin.RecoveryWithWriter(flog))
	router.Handle("GET", "/upload", uploadserver.ServeAnUpload)
	router.Handle("POST", "/upload", uploadserver.ServeAnUpload)

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
