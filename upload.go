package main //uploadserver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	errCouldNotAddToFile              = errors.New("Could not add to file")
	errCantStartDownload              = errors.New("Can not start download")
	errFileAlreadyExists              = errors.New("File is already fully downloaded")
	errCantDeletePartialFile          = errors.New("Cant delete partial file")
	errPartialFileVersionTagReadError = errors.New("Partial file version bytes read error")
	errPartialFileReadingError        = errors.New("Partial file reading error")
	errPartialFileEmpty               = errors.New("Partial file is empty")
	errPartialFileCorrupted           = errors.New("Partial file corrupted")
	errPartialFileWritingError        = errors.New("Partial file writing error")
	//--
	errServerExpectsRestOfTheFile = errors.New("Server expects rest of the file")
	errConnectionReadError        = errors.New("Connection read error")
)

//var chReciever chan []byte
var usedfiles sync.Map

type CurrentAction byte

const (
	noaction       CurrentAction = 0
	startedwriting CurrentAction = 1
	successwriting CurrentAction = 2
)
const constwriteblocklen = (1 << 16) - 1
const constdirforfiles = "d:/temp/"
const bindToAddress = "127.0.0.1:64000"

const (
	structversion1 uint32 = 0x00000022 + iota
)

type PartialFileInfo struct {
	Action      CurrentAction
	Startoffset int64
	Count       int64
}
type RequestForUpload struct {
	Filename string // Url Query parameter
}

func AddToPartialFileInfo(pfi io.Writer, step CurrentAction, info PartialFileInfo) error {
	info.Action = step

	err := binary.Write(pfi, binary.LittleEndian, &info)
	if err != nil {
		return errCouldNotAddToFile
	}
	return nil
}

func FileExists(dir, name string) bool {
	_, err := os.Open(filepath.Join(dir, name))
	return err == nil || os.IsExist(err)
}

func OpenActualFile(dir, name string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_APPEND|os.O_CREATE, 0)
	return f, err
}

func OpenTwoCorrespondentFiles(dir, name, namepart string) (wp, wa *os.File, errwp, errwa error) {
	wa, wp = nil, nil
	wp, errwp = OpenActualFile(dir, namepart)
	if errwp != nil {
		// can't start download
		return
	}

	wa, errwa = OpenActualFile(dir, name)
	if errwa != nil {
		// can't start download
		return
	}

	return

}

func ReadCurrentStateFromPartialFile(wp io.Reader) (PartialFileInfo, error) {

	var fileversion uint32
	err := binary.Read(wp, binary.LittleEndian, &fileversion)
	if err != nil {
		if err == io.EOF {
			// empty file. start filling a new one.
			return PartialFileInfo{}, errPartialFileEmpty
		}
		return PartialFileInfo{}, errPartialFileVersionTagReadError
	}
	if fileversion == structversion1 {
		//var offsetcontrol int64 // be sure offsets come in ascending order
		currrecored := PartialFileInfo{}
		prevrecored := PartialFileInfo{Action: successwriting}
		lastsuccessrecord := &prevrecored
		for {
			err := binary.Read(wp, binary.LittleEndian, &currrecored)
			if err == io.EOF {
				return *lastsuccessrecord, nil
			} else if err == io.ErrUnexpectedEOF {
				return *lastsuccessrecord, nil
			} else if err != nil {
				return prevrecored, errPartialFileReadingError // unrecoverable error
			}
			if currrecored.Startoffset >= prevrecored.Startoffset || currrecored.Startoffset != 0 {
				if prevrecored.Action == startedwriting && currrecored.Action == successwriting {
					lastsuccessrecord = &currrecored
				} else if prevrecored.Action == successwriting && currrecored.Action == startedwriting {

				} else {
					// corrupted partial file
					return *lastsuccessrecord, errPartialFileCorrupted
				}

			} else {
				// corrupted partial file
				return *lastsuccessrecord, errPartialFileCorrupted
			}
			prevrecored = currrecored

		}
	} else {
		return PartialFileInfo{}, errPartialFileVersionTagReadError
	}
}

func GetPartialFileInfo(name string) (PartialFileInfo, error) {

	namepart := name + ".partialinfo"
	dir := constdirforfiles

onemoretime:
	isPartialInfoFile := FileExists(dir, namepart)
	isActualFile := FileExists(dir, name)

	switch {
	case isPartialInfoFile && isActualFile:
		// not completed download
		wp, wa, errwp, errwa := OpenTwoCorrespondentFiles(dir, name, namepart)
		if errwp == nil {
			defer wp.Close()
		}
		if errwa == nil {
			defer wa.Close()
		}
		if errwp != nil {
			// can't start download
			return PartialFileInfo{}, errwp
		}
		if errwa != nil {
			// can't start download
			return PartialFileInfo{}, errwa
		}

		fullactualfilename := filepath.Join(dir, name)
		whatIsInFile, err := ReadCurrentStateFromPartialFile(wp)
		if err != nil {
			// can't start download
			return PartialFileInfo{}, err
		}

		wastat, err := wa.Stat()
		length := wastat.Size()
		if length > whatIsInFile.Startoffset {

			err = os.Truncate(fullactualfilename, whatIsInFile.Startoffset)
			if err != nil {
				return PartialFileInfo{}, err
			}
		} else { //length < startoffset ?? (length = 0)

			return PartialFileInfo{Startoffset: length}, nil
		}

		return whatIsInFile, nil

	case !isPartialInfoFile && !isActualFile:
		// file doesn't exist
		wp, wa, errwp, errwa := OpenTwoCorrespondentFiles(dir, name, namepart)

		if errwp == nil {
			defer wp.Close()
		}
		if errwa == nil {
			defer wa.Close()
		}
		if errwp != nil {
			// can't start download
			return PartialFileInfo{}, errwp
		}
		if errwa != nil {
			// can't start download
			return PartialFileInfo{}, errwa
		}

		ret := PartialFileInfo{
			Action:      noaction,
			Startoffset: 0,
			Count:       -1,
		}
		return ret, nil
	case !isPartialInfoFile && isActualFile:
		// the is a complete file.
		return PartialFileInfo{}, errFileAlreadyExists
	case isPartialInfoFile && !isActualFile:
		// need to restart whole file
		err := os.Remove(filepath.Join(dir, namepart))
		if err != nil {
			return PartialFileInfo{}, errCantDeletePartialFile
		}
		goto onemoretime
	}
	return PartialFileInfo{}, errCantStartDownload
}

func AddBytesToFileInHunks(wa, wp *os.File, newbytes []byte, whatIsInFile *PartialFileInfo) error {

	l := len(newbytes)
	lenhunk := constwriteblocklen
	curlen := lenhunk

	numOfhunk := l / lenhunk

	defer wp.Sync()
	defer wa.Sync()

	for i := 0; i <= numOfhunk; i++ { // steps one time more then numOfhunks
		// add step1 into journal
		err := AddToPartialFileInfo(wp, startedwriting, *whatIsInFile)
		if err != nil {
			return err
		}
		if i == numOfhunk {
			// last+1 hunk is for the rest bytes (if any)
			curlen = l - i*lenhunk
		}
		if curlen > 0 {
			// add newbytes into actual file
			nhavewritten, err := wa.WriteAt(newbytes[i*lenhunk:curlen], whatIsInFile.Startoffset)
			if err != nil {

				errFatal := wa.Truncate(whatIsInFile.Startoffset)
				if errFatal != nil {
					return errFatal
				}
				return err
			}

			// add step2 into journal
			whatIsInFile.Startoffset += int64(nhavewritten)
			err = AddToPartialFileInfo(wp, successwriting, *whatIsInFile)
			if err != nil {
				return err
			}
		}

	}
	return nil
}

func CreatePartialFileName(name string) string {
	return name + ".partialinfo"
}

func BeginNewPartialFile(dir, name string) error {
	namepart := CreatePartialFileName(name)
	wp, err := os.OpenFile(filepath.Join(dir, namepart), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0)
	if err != nil {
		return errPartialFileWritingError
	}

	defer wp.Close()
	err = binary.Write(wp, binary.LittleEndian, structversion1)
	if err != nil {
		return errPartialFileWritingError
	}
	return nil
}
func WriteAtomic(dir, name string, whatIsInFile PartialFileInfo, newbytes []byte) error {

	namepart := CreatePartialFileName(name)
	wp, err := OpenActualFile(dir, namepart)
	if err != nil {
		return errCantStartDownload
	}
	defer wp.Close()
	wa, err := OpenActualFile(dir, name)
	if err != nil {
		return errCantStartDownload
	}
	defer wa.Close()
	//----
	err = AddBytesToFileInHunks(wa, wp, newbytes, &whatIsInFile)
	if err != nil {
		return errCantStartDownload
	}
	return nil

	return errCantStartDownload
}

// RecieveAndSendToChan reads from net.Conn until io.EOF or timeout.
func RecieveAndSendToChan(c io.ReadCloser, chReciever chan []byte) error {

	// timeout set in http.Server{Timeout...}
	defer close(chReciever)

	blocklen := 3 * constwriteblocklen
	b := make([]byte, blocklen)
	i := 0
	for {

		n, err := c.Read(b) // usually reads Request.Body
		if err != nil && err != io.EOF {
			fmt.Printf("i=%d err = %s\n", i, err)
			return errConnectionReadError
		}

		if n > 0 {
			chReciever <- b[:n]
			fmt.Printf("i=%d send to chReciever n = %d\n", i, n)

		}
		if err == io.EOF {
			return io.EOF // success reading
		}

		i++
	}

}

func WriteChanneltoDisk(chSource chan []byte, chResult chan<- int, dir, name string, whatIsInFile, whatFromClient PartialFileInfo) {
	closed := false
	nbyteswritten := 0
	for !closed {
		select {
		case b, ok := <-chSource: // capacity is 3. Waits when empty.
			if !ok { // closed
				fmt.Printf("CHAN CLOSED !OK select case b, ok := <-chSource , len(b)=%d\n", len(b))
				closed = true
				// this try yields nbyteswritten bytes
				chResult <- nbyteswritten
			} else { // writes when chan is not empty
				if len(b) != 0 {
					if whatIsInFile.Startoffset == whatFromClient.Startoffset {
						fmt.Printf("WRITES BYTES len(b) = %d\n", len(b))
						WriteAtomic(dir, name, whatIsInFile, b)
						nbyteswritten += len(b)
					}

				} else {
					fmt.Printf("write 0? len(b)=%d\n", len(b))
				}

			}
			// default:
			// 	fmt.Println("step in WriteChanneltoDisk , chSource not ready\n")

		}
	}

	fmt.Println("WriteChanneltoDisk end")
	return
}

func RequestedAnUpload(c *gin.Context) {
	var req RequestForUpload
expectsrequest:
	// in URL parameter Filename is mandatory, ex.
	// curl.exe -X GET http://127.0.0.1:64000/upload?"&"Filename="sendfile.rar" -T .\sendfile.rar
	err := c.ShouldBindQuery(&req)
	if err != nil {
		//when?
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query URL must set Filename parameter"})
		return
	}

	// find file and info for the file
	fullpath := filepath.Clean(req.Filename)
	name := filepath.Base(fullpath)

	// sync.Map
	_, loaded := usedfiles.LoadOrStore(name, true)
	if loaded {
		// file is busy
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Requested filename is busy at the moment: %s", name)})
		return
	}
	defer usedfiles.Delete(name)

	// Get a struct with the file current size and state.
	// File may be partially uploaded.
	whatIsInFile, err := GetPartialFileInfo(name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return

	}
	// expects from client a file length
	lcontentstr := c.GetHeader("Content-Length")
	lcontent := 0
	if lcontentstr != "" {
		lcontent, _ = strconv.Atoi(lcontentstr)
		if lcontent == 0 {
			c.JSON(http.StatusLengthRequired, gin.H{"error": "Content-Length header required for your new file"})
			goto expectsrequest
		}
	}

	// CHANNELS for this connection
	// chReciever expects bytes to be written
	chReciever := make(chan []byte, 2)
	// chWriteResult sends nbyte written by WriteChanneltoDisk goroutine
	chWriteResult := make(chan int)

	// main switch
	if whatIsInFile.Startoffset == 0 {
		// a new file, no need for json in request
		err := BeginNewPartialFile(constdirforfiles, name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		} else {
			// GOROUTINE STARTS
			go WriteChanneltoDisk(chReciever, chWriteResult, constdirforfiles, name, whatIsInFile, whatIsInFile)
			err = RecieveAndSendToChan(c.Request.Body, chReciever)
			// here recieve has ended by timeout or by EOF
			WaitForChWriteResultAndRespond(chWriteResult, c, err, lcontent, name)
		}
	} else {
		// an existing file. We send a json with filesize. Expects from client this json back and new file bytes.
		c.JSON(http.StatusConflict, gin.H{"whatIsInFile": whatIsInFile})

		// wait from client a json answer
		var fromClient PartialFileInfo
		err := c.ShouldBindJSON(&fromClient)
		if err != nil {
			// client is trying to send a file in the body.
			// We expecting a json

			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			if fromClient.Startoffset == whatIsInFile.Startoffset {
				// client sends rest of the file
				go WriteChanneltoDisk(chReciever, chWriteResult, constdirforfiles, name, whatIsInFile, fromClient)
				err := RecieveAndSendToChan(c.Request.Body, chReciever)
				WaitForChWriteResultAndRespond(chWriteResult, c, err, int(fromClient.Startoffset), name)
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": errServerExpectsRestOfTheFile.Error(),
					"whatIsInFile": whatIsInFile})
			}
		}
	}

}

func WaitForChWriteResultAndRespond(chWriteResult chan int, c *gin.Context, err error, expectednbytes int, name string) bool {
	// asks-waits chan for the result
	nbyteswritten := 0
	select {
	// waits for chWriteResult to respond or be closed
	case nbyteswritten = <-chWriteResult:
	}

	if err == io.EOF {
		if expectednbytes != nbyteswritten {
			c.JSON(http.StatusExpectationFailed, gin.H{"error": fmt.Sprintf("Server has written %d bytes, expected number of bytes %d", nbyteswritten, expectednbytes)})

		}
		c.JSON(http.StatusOK, gin.H{})
		return true

	} else {
		c.JSON(http.StatusExpectationFailed, gin.H{"error": fmt.Sprintf("Server waited for io.EOF from you. So far server has written %d bytes to file %s", nbyteswritten, name)})
	}
	return false
	// need to close connection?
}

// to send a new file:
// curl.exe -X GET http://127.0.0.1:64000/upload?"&"Filename="sendfile.rar" -T .\sendfile.rar
func main() {

	router := gin.Default()
	router.Handle("GET", "/upload", RequestedAnUpload)

	//router.Run(bindToAddress)  timeouts needed
	s := &http.Server{
		Addr:        bindToAddress,
		Handler:     router,
		ReadTimeout: 30 * time.Second,
		//MaxHeaderBytes: 1000,
	}
	s.ListenAndServe()
	fmt.Println("main end")

	time.Sleep(10 * time.Second)
}
