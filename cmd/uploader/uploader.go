package main

// substitution is
// curl.exe -v -X POST 'http://127.0.0.1:64000/upload/zahar?&Filename="sendfile.rar"' -T .\testbackups\sendfile.rar --anyauth --user zahar
import (
	Error "upload/errstr"
	"upload/fsdriver"

	//"upload/httpDigestAuthentication"
	//"upload/liteimp"
	"upload/uploadclient"
	//"github.com/google/uuid"
	"golang.org/x/crypto/ssh/terminal"
	//"strings"

	//"crypto/sha1"
	//"encoding/json"
	"flag"
	"fmt"

	//"hash"
	//"io"
	"context"
	"log"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"

	//"strconv"
	"sync"

	"golang.org/x/net/publicsuffix"
)

var (
// errors equality by Code(or Descr), not by value, because values may be return from goroutines simultaneously.
//errServerRespondedWithBadJson = Error.E("uploader", nil, Error.ErrEncodingDecoding,"Server responded with bad json structure.")
//errStatusContinueExpected     = *errstr.NewError("uploader", 2, "We expect status 100-Continue.")
//errServerDidNotAdmitUpload    = *errstr.NewError("uploader", 8, "Server did not admit upload. We can't be sure of successfull upload.")

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
	mainCtx, callToCancel := context.WithCancel(context.Background())

	runWorkers(mainCtx, chNames)
	// here we are when runWorkers has exited
	callToCancel()
	log.Println("Normal exit.")
}

// runWorkers starts goroutines
func runWorkers(oneForAllCtx context.Context, ch chan string) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go worker(oneForAllCtx, &wg, ch)
	}
	wg.Wait()

}

// worker takes from channel and sends files
func worker(oneForAllCtx context.Context, wg *sync.WaitGroup, ch chan string) {
	defer wg.Done()

	for name := range ch {

		prepareAndSendAFile(oneForAllCtx, name, &where)
	}
	return
}

func prepareAndSendAFile(ctx context.Context, filename string, config *uploadclient.ConnectConfig) {
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
		return
	}

	err = uploadclient.SendAFile(ctx, config, fullfilename, jar, bsha1)

	if err == nil {
		log.Printf("Upload successfull: %s", fullfilename)

		if err := markFileAsUploaded(fullfilename); err != nil {
			// a non critical error
			log.Printf("%s", Error.E(op, err, errMarkFileFailed, 0, ""))
		}
		// SUCCESS
		return
	}

	log.Printf("%s", err)
	return
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
		if isarchiveset {
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
