package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"Upload/errstr"
	"Upload/uploadserver"

	"github.com/gin-gonic/gin"
)

const bindToAddress = "127.0.0.1:64000"

var (
	errCantWriteLogFile = *errstr.NewError("uploadservermain", 0, "Can not start. Can't write to a log file.")
)

func main() {
	logname := flag.String("log", "", "log file path and name.")
	// setup log destination
	var flog *os.File
	if *logname == "" {
		flog = os.Stdout
	} else {
		var err error
		flog, err = os.OpenFile(*logname, os.O_APPEND|os.O_CREATE, os.ModeAppend)
		if err != nil { // do not start without log file
			log.Fatal(errCantWriteLogFile.SetDetails("filename: %s", *logname))
		}

	}
	log.SetOutput(flog)

	router := gin.Default()
	router.Handle("GET", "/upload", uploadserver.ServeAnUpload)
	router.Handle("POST", "/upload", uploadserver.ServeAnUpload)
	router.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Output: flog,
	}))
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
