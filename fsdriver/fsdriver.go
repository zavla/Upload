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

// ReadCurrentStateFromPartialFile reads log(journal) file.
// It returns last correct log record.
// TODO: return an offset in log file where it is corrupted
func ReadCurrentStateFromPartialFile(wp io.Reader) (retState FileState, correctrecordoffset int64, errInLog error) {

	var startstruct StartStruct

	// !nil means the log-journal file must be truncated at last correct record to continue usage of this journal file
	errInLog = nil
	headersize := int64(binary.Size(startstruct)) // TODO(zavla): should be const!
	recordsize := int64(binary.Size(PartialFileInfo{}))

	// correctrecordoffset points to the end of the last correct record
	correctrecordoffset = 0 // holds journal file offset

	retState = *NewFileState(0, 0)

	// reads start bytes in file
	err := binary.Read(wp, binary.LittleEndian, &startstruct)
	if err != nil {
		if err == io.EOF {
			// empty file. No error.
			return retState,
				correctrecordoffset,
				nil
		}
		// No startstruct in header. error. File created but somehow without a startstruct header.
		return retState,
			correctrecordoffset,
			errPartialFileVersionTagReadError
	}

	// Here there is a header.
	// Check the header for correctness.
	if startstruct.VersionBytes == structversion1 && startstruct.VersionBytesEnd == structversion1 {
		correctrecordoffset += headersize
		retState.FileSize = startstruct.TotalExpectedFileLength

		currrecord := PartialFileInfo{} // current log record

		// initial value for prevrecord wil never be a match to currrecord
		prevrecord := PartialFileInfo{
			Startoffset: -1,
			Action:      successwriting} // forced to do not make a match

		lastsuccessrecord := PartialFileInfo{} // should not be a pointer
		numrecords := int64(0)
		lasterr := error(nil)
		maybeErr := false

		for { // reading records one by one
			err := binary.Read(wp, binary.LittleEndian, &currrecord)
			// next lines are dealing with bad errors while reading. But EOF is special, it means we succeded is reading.
			if err == io.ErrUnexpectedEOF { // read ended unexpectedly
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					errPartialFileCorrupted
			} else if err != nil && err != io.EOF { // read failed, use lastsuccessrecord
				//returns previous good record
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					errPartialFileReadingError // we dont know why we cant read journal. may be its ok?.
			}

			// make sure offsets come in ascending order
			// next lines are dealing with offsets in current record, decides if this record is good
			switch {
			case currrecord.Startoffset > retState.FileSize:
				// current record do not corresond to expected file size
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					errPartialFileCorrupted
			case currrecord.Startoffset == prevrecord.Startoffset &&
				currrecord.Action == successwriting &&
				prevrecord.Action == startedwriting:

				// current record found a pair. move to next record.
				numrecords++
				correctrecordoffset += recordsize * 2 // move correctrecordoffset forward 2 records (from last "correct one")
				lastsuccessrecord = currrecord        // current record is a pair. Move pointer farward.
				maybeErr = false
			case currrecord.Startoffset >= prevrecord.Startoffset &&
				currrecord.Action == startedwriting &&
				prevrecord.Action == successwriting:
				// so far so good. this is a step to next record.
				// skip this recored. it is a "start to write" record. Wait for its "pair" record.
				numrecords++
				maybeErr = true
			case err == io.EOF:

				// we reach end of file. EOF is not an error in data. We have read the last record.
				lasterr = nil
				if maybeErr {
					lasterr = errPartialFileCorrupted
				}
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					lasterr // at EOF it depends on previous record if this is corruption or no.
			default:
				// current record is bad.
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					errPartialFileCorrupted
			} // end switch
			prevrecord = currrecord // makes previous record a current one
			// continue to next record
		}
	} else {
		// incorrect header
		return retState, correctrecordoffset, errPartialFileVersionTagReadError
	}
}

//rename GetFileStatus
func MayUpload(storagepath string, name string) (FileState, error) {

	namepart := CreatePartialFileName(name)
	dir := storagepath
	wa, err := OpenRead(dir, name)
	if err != nil {

		return *NewFileState(0, 0), nil // no file, we may upload

	}
	defer wa.Close()
	wastat, err := os.Stat(filepath.Join(dir, name))
	if err != nil { // can't get actual file properties
		return *NewFileState(0, 0), errForbidenToUpdateAFile
	}

	// reads log(journal) file
	wp, err := OpenRead(dir, namepart)
	if err != nil { // != nil means no log file exists, this means actual file is already uploaded.
		// no info about actual file fullness

		return *NewFileState(wastat.Size(), wastat.Size()), errForbidenToUpdateAFile
	}
	defer wp.Close()

	// here log(journal) file exists
	fromLog, journaloffset, errlog := ReadCurrentStateFromPartialFile(wp) // here we read journal file
	// fromLog now has last correct offset == fromLog.Startoffset
	if errlog != nil { // log(journal) file needs repair
		// ReadCurrentStateFromPartialFile must return its offset of last correct record.

		if errlog == errPartialFileReadingError { // can't do anything with journal file, even reading
			return *NewFileState(fromLog.FileSize, fromLog.FileSize), errForbidenToUpdateAFile // DO NOTHING with file
		}
		// simple case is when actual file size == fromLog.startoffset and upload not completed
		if fromLog.FileSize != 0 && // log at least has filesize
			fromLog.Startoffset == wastat.Size() && // offset is equal to actual size
			fromLog.FileSize > fromLog.Startoffset { // upload not completed yet
			if err := wp.Truncate(journaloffset); err != nil {
				return *NewFileState(fromLog.FileSize, fromLog.Startoffset), errForbidenToUpdateAFile // can't repair journal
			}
			// here journal is repaired
			// may continue
		}
		// here
		return *NewFileState(wastat.Size(), wastat.Size()), errForbidenToUpdateAFile // error in log file blocks updates
	}
	if wastat.Size() == fromLog.FileSize && fromLog.Startoffset == fromLog.FileSize {
		// TODO(zavla): check SHA1 of the actual file and delete journal
		// the actual file is correct and completly uploaded, but log file still exists
		return *NewFileState(fromLog.FileSize, fromLog.Startoffset), errForbidenToUpdateAFile // totaly correct file
	}
	if wastat.Size() > fromLog.FileSize { // actual file already bigger then expected! Shire error.
		// TODO(zavla): store problemed file's journal somewhere in Db. Store SHA1 in Db.
		return *NewFileState(wastat.Size(), fromLog.Startoffset), errForbidenToUpdateAFile
	}
	if wastat.Size() == fromLog.Startoffset {
		return *NewFileState(fromLog.FileSize, fromLog.Startoffset), nil // upload may continue
	}
	// otherwise we need to read content of actual file and compare it with md5 sums of every block in journal file
	// TODO(zavla): run thorough content compare with md5 cheksums of blocks
	return *NewFileState(wastat.Size(), fromLog.Startoffset), errForbidenToUpdateAFile
}
