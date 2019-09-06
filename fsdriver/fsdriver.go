package fsdriver

import (
	"Upload/errstr"
	"crypto/sha1"
	"encoding/binary"
	"io"
	"log"
	"os"
	"path/filepath"
)

//-----has own errors

//--------------------

var (
	errCouldNotAddToFile                = *errstr.NewError("fsdriver", 1, "Could not add to file")
	errPartialFileVersionTagReadError   = *errstr.NewError("fsdriver", 5, "Transaction Log file header read error.")
	errPartialFileReadingError          = *errstr.NewError("fsdriver", 6, "Transaction Log file reading error.")
	errPartialFileEmpty                 = *errstr.NewError("fsdriver", 7, "Transaction Log file is empty.")
	errPartialFileCorrupted             = *errstr.NewError("fsdriver", 8, "Transaction Log file corrupted.")
	errPartialFileWritingError          = *errstr.NewError("fsdriver", 9, "Transaction Log file writing error.")
	errFileOpenForReadWriteError        = *errstr.NewError("fsdriver", 10, "Open file for read and write error.")
	errForbidenToUpdateAFile            = *errstr.NewError("fsdriver", 11, "File already exists, can't overwrite.")
	errPartialFileCantDelete            = *errstr.NewError("fsdriver", 12, "Transaction Log file unable to delete.")
	errPartialFileVersionTagUnsupported = *errstr.NewError("fsdriver", 13, "Transaction Log file version unsupported.")
)

const constwriteblocklen = (1 << 16) - 1

type currentAction byte

const (
	noaction       currentAction = 0
	startedwriting currentAction = 1
	successwriting currentAction = 2
)

// ---------------Log file versions BEGIN
// next is versions of journal file
const (
	structversion1 uint32 = 0x00000022 + iota // defines log records version, if version changes between downloads
	structversion2
)

// what is the last journal file version this fsdriver supports?
const supportsLatestVer uint32 = structversion2

// ---------------Log file versions END

// next go several versions of JournalRecordXXX

// JournalRecord is a record in log file.
// This is always the latest version than can hold all old versions of record.
type JournalRecord struct {
	Action      currentAction
	Startoffset int64
	Count       int64
	Crc32       int32
}

// JournalRecordVer1 this is a record version 1
type JournalRecordVer1 struct {
	Action      currentAction
	Startoffset int64
	Count       int64
}

// Ver1 translates JournalRecord to old version 1
func (r JournalRecord) Ver1() JournalRecordVer1 {
	return JournalRecordVer1{
		Action:      r.Action,
		Startoffset: r.Startoffset,
		Count:       r.Count,
	}
}

// JournalRecordVer2 this is a record version 2
type JournalRecordVer2 struct {
	Action      currentAction
	Startoffset int64
	Count       int64
	Crc32       int32
}

// Ver2 translates JournalRecord to old version 2
func (r JournalRecord) Ver2() JournalRecordVer2 {
	return JournalRecordVer2{
		Action:      r.Action,
		Startoffset: r.Startoffset,
		Count:       r.Count,
		Crc32:       r.Crc32,
	}
}

type fileProperties struct {
	FileSize int64
	Sha1     []byte
}

// FileState used to return state of a file to other packages
type FileState struct {
	fileProperties
	Startoffset int64
}

// NewFileState creates FileState with parameters
func NewFileState(filesize int64, bsha1 []byte, startoffset int64) *FileState {
	return &FileState{
		fileProperties: fileProperties{FileSize: filesize, Sha1: bsha1},
		Startoffset:    startoffset,
	}
}
func (s *FileState) Setoffset(offset int64) *FileState {
	//reciever modified
	s.Startoffset = offset
	return s
}

//---------------StartStruct versions
// StartStruct is a start record in log file.
// Holds journal file version number
// This a a header of the journal file.
// StartStruct can hold every old version @ver.
type StartStruct struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	Sha1                    [20]byte
	VersionBytesEnd         uint32
}
type StartStructVer1 struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	VersionBytesEnd         uint32
}
type StartStructVer2 struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	Sha1                    [20]byte
	VersionBytesEnd         uint32
}

func (s StartStruct) Ver1() StartStructVer1 {
	return StartStructVer1{
		VersionBytes:            s.VersionBytes,
		VersionBytesEnd:         s.VersionBytesEnd,
		TotalExpectedFileLength: s.TotalExpectedFileLength,
	}
}
func (s StartStruct) Ver2() StartStructVer2 {
	return StartStructVer2{
		VersionBytes:            s.VersionBytes,
		VersionBytesEnd:         s.VersionBytesEnd,
		TotalExpectedFileLength: s.TotalExpectedFileLength,
		Sha1:                    s.Sha1,
	}

}

//---------------StartStruct versions

func NewStartStruct() *StartStruct {
	return &StartStruct{
		VersionBytes:    structversion1,
		VersionBytesEnd: structversion1,
	}
}

//---------------------------------
// GetLogFileName returns a log filename.
// TODO: rename GetLogFileName
func GetPartialJournalFileName(name string) string {
	return name + ".partialinfo"
}

//rename to CreateNewLogFile
func BeginNewPartialFile(dir, name string, lcontent int64, bytessha1 []byte) error {
	namepart := GetPartialJournalFileName(name)
	wp, err := os.OpenFile(filepath.Join(dir, namepart), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0)
	if err != nil {
		return errPartialFileWritingError
	}

	defer wp.Close()
	// version of journal file
	err = binary.Write(wp, binary.LittleEndian, supportsLatestVer)
	if err != nil {
		return errPartialFileWritingError
	}
	// fills header of journal file
	aLogHeader := NewStartStruct()
	aLogHeader.TotalExpectedFileLength = lcontent
	copy(aLogHeader.Sha1[:], bytessha1)

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
	// seeks END
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_APPEND|os.O_CREATE, 0)
	return f, err
}

// opens journal file and seeks offset 0 to read version then seeks END
func OpenJournalFile(dir, name string) (*os.File, uint32, error) {
	// opens at the BEGINING
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0)
	if err != nil {
		return f, 0, err
	}
	ver, err := GetJournalFileVersion(f)
	if err != nil {
		// do not seek END on error
		return f, 0, err
	}
	_, err = f.Seek(0, 2) // END !!!
	if err != nil {
		// can't seek = can't use file
		return f, 0, err
	}
	return f, ver, err
}

//rename to PrepareFilesForWrite
func OpenTwoCorrespondentFiles(dir, name, namepart string) (ver uint32, wp, wa *os.File, errwp, errwa error) {
	wa, wp = nil, nil // named return
	wp, ver, errwp = OpenJournalFile(dir, namepart)
	if errwp != nil {
		return
	}

	wa, errwa = OpenActualFile(dir, name)
	if errwa != nil {
		return
	}
	return //named ver, wp, wa, errwp, errwa
}

// AddBytesToFile writes to log file first and appends only to the actual file.
// Writes to actual file in blocks (hunks).
// Used by other packages.
func AddBytesToFile(wa, wp *os.File, newbytes []byte, ver uint32, destinationrecord *JournalRecord) (int64, error) {
	// ver is a journal file version
	l := len(newbytes)
	lenhunk := constwriteblocklen // the size of block
	curlen := lenhunk

	hunkscount := l / lenhunk

	// TODO(zavla): don't do Sync when hunkscount == 0 ?
	defer wp.Sync() // log file sync
	defer wa.Sync() // actual file sync

	totalbyteswritten := int64(0)
	for i := 0; i <= hunkscount; i++ { // steps one time more then numOfhunks, last hunk is not full.
		// dicides what is the current block length
		if i == hunkscount {
			// last+1 hunk is for the rest of bytes (if any)
			curlen = l - i*lenhunk
		}
		if curlen > 0 {
			// add step1 into journal = "write begin"
			destinationrecord.Count = int64(curlen)
			err := addRecordToJournalFile(wp, startedwriting, ver, *destinationrecord)
			if err != nil {
				return totalbyteswritten, err
			}

			// add newbytes into actual file
			from := i * lenhunk
			to := from + curlen

			minlen := 16
			if minlen > curlen {
				minlen = curlen
			}

			nhavewritten, err := wa.WriteAt(newbytes[from:to], destinationrecord.Startoffset)
			// err == nil when FSD (file system driver) accepted data but still holds it in memory till flush
			if err != nil {
				// write of current block (hunk) failed
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
			err = addRecordToJournalFile(wp, successwriting, ver, *destinationrecord)
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

// addRecordToJournalFile writes binary representation of PartialFileInfo into log file.
func addRecordToJournalFile(pfi io.Writer, step currentAction, ver uint32, info JournalRecord) error {
	// ver is a journal file version
	// PartialFileInfo is always can hold all old versions.
	// We transform it to a correct version.
	info.Action = step

	var err error
	switch ver {
	case structversion2:
		infoVer2 := info.Ver2()
		err = binary.Write(pfi, binary.LittleEndian, &infoVer2)
	case structversion1:
		infoVer1 := info.Ver1()
		err = binary.Write(pfi, binary.LittleEndian, &infoVer1)
	default:
		return errPartialFileVersionTagUnsupported
	}

	if err != nil {
		return errCouldNotAddToFile
	}
	return nil
}

// GetJournalFileVersion returns version of a journal file.
// wp current pointer moved forward by uint32 bytes.
func GetJournalFileVersion(wp io.Reader) (uint32, error) {
	var ret uint32
	// what is the version of log file?

	err := binary.Read(wp, binary.LittleEndian, &ret)
	if err == io.EOF {
		// empty file. No error.
		return supportsLatestVer, nil //uses last supported by this fsdriver version
	}
	if err != nil {
		return 0, errPartialFileReadingError
	}
	if ret < structversion1 || ret > supportsLatestVer {
		return 0, errPartialFileVersionTagUnsupported
	}
	return ret, nil // supported version
}

// MayUpload decides if this file may be overwritten.
// It looks for correspondent journal file for this file,
// and use it to get a file state of the file being uploaded.
func MayUpload(storagepath string, name string) (FileState, error) {

	namepart := GetPartialJournalFileName(name)

	wa, err := OpenRead(storagepath, name)
	if err != nil {
		// no uploaded file, client may upload
		return *NewFileState(0, nil, 0), nil

	}
	defer wa.Close()
	wastat, err := os.Stat(filepath.Join(storagepath, name))
	if err != nil {
		// can't get actual file properties, error
		return *NewFileState(0, nil, 0), errForbidenToUpdateAFile
	}

	// reads log(journal) file
	wp, err := OpenRead(storagepath, namepart)
	if err != nil {
		// != nil means no log file exists, this means actual file is already uploaded
		// (otherwise it has a journal file near)
		// or there is a reading error

		return *NewFileState(wastat.Size(), nil, wastat.Size()), errForbidenToUpdateAFile
	}
	defer wp.Close()

	ver, err := GetJournalFileVersion(wp)
	if err != nil { // unsupported version or read error
		return *NewFileState(0, nil, 0), errForbidenToUpdateAFile
	}

	var fromLog FileState
	var journaloffset int64
	var errlog error

	// here log(journal) file already exists
	// every ReadCurrentStateFromJournalVerX must return its offset of last correct journal record.
	switch ver {
	case structversion1:
		// here we read journal file
		fromLog, journaloffset, errlog = ReadCurrentStateFromJournalVer1(ver, wp)

	case structversion2:
		// here we read journal file
		fromLog, journaloffset, errlog = ReadCurrentStateFromJournalVer2(ver, wp)
	default: // unknown version
		return *NewFileState(0, nil, 0), errForbidenToUpdateAFile
	}

	if errlog == errPartialFileReadingError { // can't do anything with journal file, even reading
		return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.FileSize), errForbidenToUpdateAFile // DO NOTHING with file
	}

	// Here struct fromLog has last correct offset of the file being uploaded, fromLog.Startoffset.
	if errlog != nil {
		// log(journal) file needs repair.
		// simple case is when actual file size == fromLog.startoffset
		// It is when an upload did not complete.
		if fromLog.FileSize != 0 && // log at least has filesize
			fromLog.Startoffset == wastat.Size() && // offset is equal to actual file size
			fromLog.FileSize > fromLog.Startoffset { // upload not completed yet
			// may be that uploadserver was shutted down in the process of adding to journal file
			// try to repair journal file to lcontinue upload
			if err := wp.Truncate(journaloffset); err == nil { // ==
				// here journal is repaired
				// may continue
				return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset), nil
			}
		}

		// here journal file is considdered to be in corrupted state
		return *NewFileState(wastat.Size(), fromLog.Sha1, wastat.Size()), errForbidenToUpdateAFile // error in log file blocks updates
	}
	if wastat.Size() == fromLog.FileSize && fromLog.Startoffset == fromLog.FileSize {
		// the actual file is correct and completly uploaded,
		// but partial journal file still exists.
		// TODO: move journal somewhere?
		return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset), errForbidenToUpdateAFile // totaly correct file
	}
	if wastat.Size() > fromLog.FileSize {
		// actual file already bigger then expected! Impossible unless a user has intervened.
		log.Printf("Аctual file already bigger then expected: %s has %d bytes, journal says %d bytes.", name, wastat.Size(), fromLog.Startoffset)
		return *NewFileState(wastat.Size(), fromLog.Sha1, fromLog.Startoffset), errForbidenToUpdateAFile
	}
	if wastat.Size() == fromLog.Startoffset {
		// upload may continue
		return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset), nil
	}
	// otherwise we need to read content of actual file and compare it with crc64 sums of every block in journal file
	// TODO(zavla): run thorough content compare with md5 cheksums of blocks
	return *NewFileState(wastat.Size(), fromLog.Sha1, fromLog.Startoffset), errForbidenToUpdateAFile
}

// GetFileSha1 gets sha1 of a file as a []byte
func GetFileSha1(storagepath, name string) ([]byte, error) {
	var ret []byte
	f, err := os.Open(filepath.Join(storagepath, name))
	if err != nil {
		return ret, err
	}
	defer f.Close()

	h := sha1.New()
	_, err = io.Copy(h, f)
	if err != nil {
		return ret, err
	}
	return h.Sum(nil), nil
}
