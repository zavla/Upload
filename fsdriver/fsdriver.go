package fsdriver

import (
	"Upload/errstr"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
)

//-----has own errors

//--------------------

var (
	errCouldNotAddToFile              = *errstr.NewError("fsdriver", 1, "Could not add to file")
	errPartialFileVersionTagReadError = *errstr.NewError("fsdriver", 5, "Transaction Log file header read error.")
	errPartialFileReadingError        = *errstr.NewError("fsdriver", 6, "Transaction Log file reading error.")
	errPartialFileEmpty               = *errstr.NewError("fsdriver", 7, "Transaction Log file is empty.")
	errPartialFileCorrupted           = *errstr.NewError("fsdriver", 8, "Transaction Log file corrupted.")
	errPartialFileWritingError        = *errstr.NewError("fsdriver", 9, "Transaction Log file writing error.")
	errFileOpenForReadWriteError      = *errstr.NewError("fsdriver", 10, "Open file for read and write error.")
	errForbidenToUpdateAFile          = *errstr.NewError("fsdriver", 11, "File already exists, can't overwrite.")
	errPartialFileCantDelete          = *errstr.NewError("fsdriver", 12, "Transaction Log file unable to delete.")
)

const constwriteblocklen = (1 << 16) - 1

type CurrentAction byte

const (
	noaction       CurrentAction = 0
	startedwriting CurrentAction = 1
	successwriting CurrentAction = 2
)

// ---------------Log file records
const (
	structversion1 uint32 = 0x00000022 + iota // defines log records version, if version changes between downloads
)

// PartialFileInfo is a record in log file.
type PartialFileInfo struct {
	Action      CurrentAction
	Startoffset int64
	Count       int64
}
type FileProperties struct {
	FileSize int64
}
type FileState struct {
	FileProperties
	Startoffset int64
}

func NewFileState(filesize int64, startoffset int64) *FileState {
	return &FileState{FileProperties: FileProperties{FileSize: filesize}, Startoffset: startoffset}
}
func (s *FileState) Setoffset(offset int64) *FileState {
	//reciever modified
	s.Startoffset = offset
	return s
}

//var FileStateGlob FileState // global var for static use

// StartStruct is a start record in log file.
// Holds version number for struct PartialFileInfo and TotalLehgth of the file
// This a a header of the partial file.
type StartStruct struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	VersionBytesEnd         uint32
}

func NewStartStruct() *StartStruct {
	return &StartStruct{
		VersionBytes:    structversion1,
		VersionBytesEnd: structversion1,
	}
}

//---------------------------------
// GetLogFileName returns a log filename.
// TODO: rename GetLogFileName
func CreatePartialFileName(name string) string {
	return name + ".partialinfo"
}

//rename to CreateNewLogFile
func BeginNewPartialFile(dir, name string, lcontent int64) error {
	namepart := CreatePartialFileName(name)
	wp, err := os.OpenFile(filepath.Join(dir, namepart), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0)
	if err != nil {
		return errPartialFileWritingError
	}

	defer wp.Close()
	aLogHeader := NewStartStruct()
	aLogHeader.TotalExpectedFileLength = lcontent
	err = binary.Write(wp, binary.LittleEndian, *aLogHeader)
	if err != nil {
		return errPartialFileWritingError
	}
	return nil
}

func OpenRead(dir, name string) (*os.File, error) {
	f, err := os.Open(filepath.Join(dir, name))
	return f, err
}

// rename to OpenWriteCreate
func OpenActualFile(dir, name string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_APPEND|os.O_CREATE, 0)
	return f, err
}

//rename to PrepareFilesForWrite
func OpenTwoCorrespondentFiles(dir, name, namepart string) (wp, wa *os.File, errwp, errwa error) {
	wa, wp = nil, nil // named return
	wp, errwp = OpenActualFile(dir, namepart)
	if errwp != nil {
		return
	}

	wa, errwa = OpenActualFile(dir, name)
	if errwa != nil {
		return
	}
	return //named wp, wa, errwp, errwa
}

// AddBytesToFileInHunks writes to log file first and appends only to the actual file.
// Writes to actual file in blocks (hunks).
//rename AddBytesToAFileUseTransactionLog
func AddBytesToFileInHunks(wa, wp *os.File, newbytes []byte, destinationrecord *PartialFileInfo) (int64, error) {

	l := len(newbytes)
	lenhunk := constwriteblocklen
	curlen := lenhunk

	numOfhunk := l / lenhunk

	defer wp.Sync() // mandatory log file sync
	defer wa.Sync() // mandatory actual file sync

	totalbyteswritten := int64(0)
	for i := 0; i <= numOfhunk; i++ { // steps one time more then numOfhunks, last hunk is not full.
		// add step1 into journal = "write begin"
		err := AddToPartialFileInfo(wp, startedwriting, *destinationrecord)
		if err != nil {
			return totalbyteswritten, err
		}
		if i == numOfhunk {
			// last+1 hunk is for the rest of bytes (if any)
			curlen = l - i*lenhunk
		}
		if curlen > 0 {
			// add newbytes into actual file
			nhavewritten, err := wa.WriteAt(newbytes[i*lenhunk:curlen], destinationrecord.Startoffset)
			if err != nil {
				// write of current block (hunk) failed, not big deal.
				// revert actual file, though it's not necessary, because log file doesn't have step2 record.
				errFatal := wa.Truncate(destinationrecord.Startoffset)
				if errFatal != nil { // can't truncate actual file.
					return totalbyteswritten, errFatal
				}
				return totalbyteswritten, err // file reverted, and will continue after failer cause elumination (disk space freed for e.x.).
			}

			// add step2 into journal = "write end"
			// whatIsInFile.Startoffset looks like transaction number
			destinationrecord.Count = int64(nhavewritten)
			err = AddToPartialFileInfo(wp, successwriting, *destinationrecord)
			if err != nil {
				destinationrecord.Count = 0
				return totalbyteswritten, err // log file failer. (free disk space for e.x.)
			}
			destinationrecord.Startoffset += int64(nhavewritten) // finaly adds writen bytes count to offset
			totalbyteswritten += int64(nhavewritten)
		}

	}
	return totalbyteswritten, nil
}

// AddToPartialFileInfo writes binary representation of PartialFileInfo into log file.
func AddToPartialFileInfo(pfi io.Writer, step CurrentAction, info PartialFileInfo) error {

	info.Action = step
	err := binary.Write(pfi, binary.LittleEndian, &info)
	if err != nil {
		return errCouldNotAddToFile
	}
	return nil
}

// ReadCurrentStateFromPartialFile reads header of type StartStruct.
// Then reads PartialFileInfo structs from log file.
// Validate log file and returns last correct log record.
// TODO: return on offset in log file where it is corrupted
func ReadCurrentStateFromPartialFile(wp io.Reader) (retState FileState, errInLog error) {

	var startstruct StartStruct
	errInLog = nil
	retState = *NewFileState(0, 0)

	err := binary.Read(wp, binary.LittleEndian, &startstruct)
	if err != nil {
		if err == io.EOF {
			// empty file. No startstruct in header. Start filling a new one.
			return retState, errPartialFileEmpty
		}
		return retState, errPartialFileVersionTagReadError
	}
	// there is a header
	// checks the header for correctness
	if startstruct.VersionBytes == structversion1 && startstruct.VersionBytesEnd == structversion1 {
		retfilestate := NewFileState(startstruct.TotalExpectedFileLength, 0)
		// make sure offsets come in ascending order
		currrecored := PartialFileInfo{} // current log record
		prevrecored := PartialFileInfo{Action: successwriting,
			Startoffset: 0,
			Count:       startstruct.TotalExpectedFileLength} // previous log record
		lastsuccessrecord := PartialFileInfo{} // should not be pointer                                                                           //address of empty record
		for {
			err := binary.Read(wp, binary.LittleEndian, &currrecored)

			// DEBUG!!!
			if err == io.EOF { // we reach end of file
				return *retfilestate.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count), nil
			} else if err == io.ErrUnexpectedEOF { // read ended unexpectedly
				return *retfilestate.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count), nil
			} else if err != nil { // read faild, use lastsuccessrecord
				//returns previous good record
				return *retfilestate.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count), errPartialFileReadingError // unrecoverable error
			}
			//+
			if currrecored.Startoffset == prevrecored.Startoffset &&
				currrecored.Action == successwriting &&
				prevrecored.Action == startedwriting {
				// lastsuccessrecord only changes here
				lastsuccessrecord = currrecored // current record is a pair. Move pointer farward.
			} else if currrecored.Startoffset >= prevrecored.Startoffset {
				if currrecored.Action == startedwriting && prevrecored.Action == successwriting {
					// skip this recored. it is a "start to write" record. Wait for its "pair" record.
				} else {
					// corrupted partial file
					return *retfilestate.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count), errPartialFileCorrupted
				}

			} else {
				// corrupted partial file. Previous offset is bigger then current.
				return *retfilestate.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count), errPartialFileCorrupted
			}
			prevrecored = currrecored // makes previous record a current one

		}
	} else {
		// incorrect header
		return retState, errPartialFileVersionTagReadError
	}
}

//rename GetFileStatus
func MayUpload(storagepath string, name string) (FileState, error) {

	namepart := CreatePartialFileName(name)
	dir := storagepath
	wa, err := OpenRead(dir, name)
	if err != nil {

		return *NewFileState(0, 0), nil // no file, may upload

	}
	defer wa.Close()
	wastat, err := os.Stat(filepath.Join(dir, name))
	if err != nil { // can't get actual file properties
		return *NewFileState(0, 0), errForbidenToUpdateAFile
	}

	// next reads log file
	wp, err := OpenRead(dir, namepart)
	if err != nil { // != nil means no log file, means actual file already uploaded.

		return *NewFileState(wastat.Size(), wastat.Size()), errForbidenToUpdateAFile
	}
	defer wp.Close()
	// log file exists
	fromLog, errlog := ReadCurrentStateFromPartialFile(wp)
	if errlog != nil {
		// log has file length and minimum correct offset, thougth may have further errors
		if fromLog.FileSize != 0 && // log has something
			fromLog.Startoffset != 0 && // log has some offset
			fromLog.Startoffset <= wastat.Size() { // log has offset that may be trusted, it's <= then actual filesize
			if fromLog.Startoffset < wastat.Size() {
				return *NewFileState(fromLog.FileSize, fromLog.Startoffset), nil // you may continue
			}
			return *NewFileState(wastat.Size(), wastat.Size()), errForbidenToUpdateAFile // upload copmleted? block updates
		}
		return *NewFileState(wastat.Size(), wastat.Size()), errForbidenToUpdateAFile // error in log file blocks updates
	}
	if wastat.Size() == fromLog.FileSize && fromLog.Startoffset == fromLog.FileSize {
		// the actual file is correct and completly uploaded, but log file still exists
		return *NewFileState(fromLog.FileSize, fromLog.Startoffset), errForbidenToUpdateAFile // totaly correct file
	}
	return *NewFileState(fromLog.FileSize, fromLog.Startoffset), nil // upload may continue

}
