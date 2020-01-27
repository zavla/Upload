package main

// substitution is
// curl.exe -v -X POST 'http://127.0.0.1:64000/upload/zahar?&Filename="sendfile.rar"' -T .\testbackups\sendfile.rar --anyauth --user zahar
import (
	"time"
	Error "upload/errstr"
	"upload/fsdriver"

	"upload/uploadclient"

	"flag"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"

	"context"
	"log"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"

	"sync"

	"golang.org/x/net/publicsuffix"
	
)

var where uploadclient.ConnectConfig

func main() {
	const op = "main()"
	logname := flag.String("log", "", "log file.")
	file := flag.String("file", "", "a file you want to upload.")
	dirtomonitor := flag.String("dir", "", "a directory which to upload.")
	username := flag.String("username", "", "user name of an Upload service.")
	uploadServerURL := flag.String("service", `http://127.0.0.1:64000/upload`, "URL of the Upload service.")
	askpassword := flag.Bool("askpassword", true, "will ask your password for the Upload service.")
	//passwordfile := flag.String("passwordfile","","a file with password (Windows DPAPI encrypted).")

	flag.Parse()
	if len(os.Args[1:]) == 0 {
		flag.PrintDefaults()
		os.Exit(1)
		return
	}
	// check required parameters
	where.ToURL = *uploadServerURL

	if *file == "" && *dirtomonitor == "" {
		log.Printf("--file or --dir must be specified.")
		os.Exit(1)
		return
	}
	// asks password only if there is a specified user
	if *askpassword && *username != "" {
		fmt.Printf("\nEnter user '%s' password: ", *username)
		password, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		println("")
		if err != nil {
			fmt.Printf("Reading password error: %s", err)
			return
		}
		where.Password = string(password)
	}
	if *username != "" {
		where.Username = *username
		if where.ToURL[len(where.ToURL)-1] != '/' {
			where.ToURL += "/"
		}
		where.ToURL += *username
	}
	
	defer func() {
		// on panic we will write to log file
		if err := recover(); err != nil {

			log.Printf("uploader main has paniced:\n%s\n", err)
			b := make([]byte, 2500) // enough buffer
			n := runtime.Stack(b, true)
			b = b[:n]
			// logs stack trace
			log.Printf("%d bytes of stack trace.\n%s", n, string(b))
		}
	}()
	// setup log destination
	var flog *os.File
	if *logname == "" {
		flog = os.Stdout
	} else {
		var err error
		flog, err = os.OpenFile(*logname, os.O_APPEND|os.O_CREATE, os.ModeAppend)
		log.Printf("%s", Error.E(op, err, errCantOpenFileForReading, 0, ""))
		os.Exit(1)
		return
	}

	// from hereafter use log for messages
	log.SetOutput(flog)
	defer func() { _ = flog.Close() }()

	// channel with filenames
	chNames := make(chan string, 2)

	if *file != "" {
		// send to chNames
		chNames <- *file
	}
	if *dirtomonitor != "" {
		// walk a dir, send names to chNames
		go getFilenames(*dirtomonitor, chNames) // closes chNames after adding all files
	} else {
		close(chNames)
	}
	// TODO(zavla): import "github.com/fsnotify/fsnotify"
	// TODO(zavla): store partial files aside? move them somewhere?
	// TODO(zavla): run uploadserver as a Windows service.
	// TODO(zavla): autotls?
	// TODO(zavla): CSRF, do not mix POST request with URL parametres!
	// TODO(zavla): in server calculate speed of upload.
	mainCtx, callmeToCancel := context.WithCancel(context.Background())

	runWorkers(mainCtx, callmeToCancel, chNames)
	// here we are when runWorkers has exited

	log.Println("Normal exit.")
}

// runWorkers starts payload goroutines.
// Every gorouting is then allowed to cancel the whole request after encouning an athorization error.
func runWorkers(oneForAllCtx context.Context, callmeToCancel context.CancelFunc, ch chan string) {
	var wg sync.WaitGroup
	be1 := true
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go worker(oneForAllCtx, callmeToCancel, &wg, ch)

		// pause after first worker has been run
		if be1 {

			time.Sleep(time.Millisecond * 200)
			be1 = false
		}
	}
	wg.Wait()

}

// worker takes from channel and sends files.
// This peticular workers ARE allowed to cancel the whole context oneForAllCtx in case of authorization error.
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
			log.Printf("cancelling the whole request")
			return
			// select {
			// case _, ok := <-ch:
			// 	if !ok {
			// 		close(ch) // cancels other workers, authorization failed.

			// 	}
			// }
		}
	}
	return
}

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
		log.Printf("%s", Error.E(op, err, errCantOpenFileForReading, 0, ""))
		return err
	}

	err = uploadclient.SendAFile(ctx, config, fullfilename, jar, bsha1)

	if err == nil {
		log.Printf("Upload successfull: %s", fullfilename)

		if err := markFileAsUploaded(fullfilename); err != nil {
			// a non critical error
			log.Printf("%s", Error.E(op, err, errMarkFileFailed, 0, ""))
		}
		// SUCCESS
		return nil
	}

	log.Printf("%s", err)
	return err // every error is returned upward, including authorization error.
}

// getFilenames collects files names in a directory and sends them to channel chNames.
// If "archive" file attribute on Windows and FS_NODUMP_FL file attribute on linux is set
// then the file will be chosen.
func getFilenames(dir string, chNames chan<- string) {
	const op = "uploader.getFilenamesToupload()"
	defer close(chNames)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil // next file please
		}
		// uses "archive" attribute on Windows and FS_NODUMP_FL file attribute on linux.
		isarchiveset, _ := getArchiveAttribute(path)
		if isarchiveset ||
			true { //DEBUG!!!
			chNames <- path
		}
		return nil // next file please
	})
	if err != nil {
		close(chNames)
		log.Printf("%s", Error.E(op, err, errReadingDirectory, 0, ""))
	}

	return
}
