package fsdriver

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	Error "upload/errstr"
)

const constwriteblocklen = (1 << 16) - 1

type currentAction byte

const (
	noaction       currentAction = 0
	startedwriting currentAction = 1
	successwriting currentAction = 2
)

// VERSIONS of journal
const (
	structversion1 uint32 = 0x00000022 + iota // defines log records version, if version changes between downloads
	structversion2
)

// This fsdriver supports:
// Must be set by programmer in case of journal version change.
const supportsLatestVer uint32 = structversion2

// JournalRecord is a record in log file.
// This struct always can hold all old versions of journal records.
// Used as a way to pass all versions between functions.
type JournalRecord struct {
	Action      currentAction
	Startoffset int64
	Count       int64
	Crc32       int32
}

// startstruct is a header record of journal file.
// startstruct can hold every old version of journal header.
// Used to pass header between functions.
type startstruct struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	Sha1                    [20]byte
	VersionBytesEnd         uint32
}

// newStartStruct returns header for new journal file, which will be of the last version.
func newStartStruct() *startstruct {
	return &startstruct{
		VersionBytes:    supportsLatestVer,
		VersionBytesEnd: supportsLatestVer,
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

// NewFileState creates FileState with parameters. A kind of constructor.
func NewFileState(filesize int64, bsha1 []byte, startoffset int64) *FileState {
	return &FileState{
		fileProperties: fileProperties{FileSize: filesize, Sha1: bsha1},
		Startoffset:    startoffset,
	}
}

// Setoffset is a setter
func (s *FileState) Setoffset(offset int64) *FileState {
	//reciever is modified
	s.Startoffset = offset
	return s
}

// GetPartialJournalFileName returns a log filename.
// TODO: rename
func GetPartialJournalFileName(name string) string {
	return name + ".partialinfo"
}

// CreateNewPartialJournalFile creates new journal file and writes a journal header.
// dir must be created before.
func CreateNewPartialJournalFile(dir, name string, lcontent int64, bytessha1 []byte) error {
	const op = "fsdriver.CreateNewPartialJournalFile()"

	namepart := GetPartialJournalFileName(name)
	wp, err := os.OpenFile(filepath.Join(dir, namepart), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0)
	if err != nil {
		return Error.E(op, err, errPartialFileCreate, 0, "")
	}

	defer wp.Close()
	// version of journal file
	err = binary.Write(wp, binary.LittleEndian, supportsLatestVer)
	if err != nil {
		return Error.E(op, err, errPartialFileWritingError, 0, "")
	}
	// fills header of journal file
	aLogHeader := newStartStruct()
	aLogHeader.TotalExpectedFileLength = lcontent
	copy(aLogHeader.Sha1[:], bytessha1)

	err = binary.Write(wp, binary.LittleEndian, *aLogHeader)
	if err != nil {
		return Error.E(op, err, errPartialFileWritingError, 0, "")
	}
	return nil
}

func openToRead(dir, name string) (*os.File, error) {
	f, err := os.Open(filepath.Join(dir, name))
	return f, err
}

func openToWrite(dir, name string) (*os.File, error) {
	// seeks END
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_APPEND|os.O_CREATE, 0)
	return f, err
}

// openJournalFile opens journal(log) file and seeks offset 0 to read a version struct and then seeks END of the file.
func openJournalFile(dir, name string) (*os.File, uint32, error) {
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
	_, err = f.Seek(0, 2) // seeks from the END !!!
	if err != nil {
		// can't seek = can't use file
		return f, 0, err
	}
	return f, ver, err
}

// OpenTwoCorrespondentFiles opens two files for writing.
func OpenTwoCorrespondentFiles(dir, name, namepart string) (ver uint32, wp, wa *os.File, errwp, errwa error) {
	wa, wp = nil, nil // named return, wa=actual, wp=partial(that is log file)
	wp, ver, errwp = openJournalFile(dir, namepart)
	if errwp != nil {
		return
	}

	wa, errwa = openToWrite(dir, name)
	if errwa != nil {
		return
	}
	return //named ver, wp, wa, errwp, errwa
}

// AddBytesToFile writes to journal file then appends newbytes to the actual file.
// Writes to actual file using blocks (hunks). Write each block. Block is a write unit.
// Used by package uploadserver.
func AddBytesToFile(wa, wp *os.File, newbytes []byte, ver uint32, destinationrecord *JournalRecord) (int64, error) {
	// ver is a journal file version
	l := len(newbytes)
	lenhunk := constwriteblocklen // the size of block
	curlen := lenhunk

	hunkscount := l / lenhunk

	// TODO(zavla): don't do Sync when hunkscount == 0 ?
	defer wp.Sync() // journal file sync
	defer wa.Sync() // actual file sync

	totalbyteswritten := int64(0)
	for i := 0; i <= hunkscount; i++ { // steps one time more then numOfhunks, last hunk is not full.
		// dicides what is the current block length
		if i == hunkscount {
			// last+1 hunk is for the rest of bytes (if any)
			curlen = l - i*lenhunk
		}
		if curlen > 0 {
			// newbytes[from:to] into actual file
			from := i * lenhunk
			to := from + curlen
			// TODO(zavla): CRC32 newbytes[from:to] !!!
			// OR sha1 newbytes and store a file with name==sha1 of a block
			// add step1 into journal = "write begin"
			destinationrecord.Count = int64(curlen)
			err := addRecordToJournalFile(wp, startedwriting, ver, *destinationrecord)
			if err != nil {
				return totalbyteswritten, err
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
				return totalbyteswritten, err // log file failure. (no free disk space for e.x.)
			}
			destinationrecord.Startoffset += int64(nhavewritten) // finaly adds writen bytes count to offset
			totalbyteswritten += int64(nhavewritten)
		}

	}
	return totalbyteswritten, nil
}

// addRecordToJournalFile writes binary representation of PartialFileInfo into log file.
func addRecordToJournalFile(pfi io.Writer, step currentAction, ver uint32, info JournalRecord) error {
	const op = "fsdriver.addRecordToJournalFile()"
	// ver is a journal file version
	// PartialFileInfo is always can hold all old versions.
	// We transform it to a correct version.
	info.Action = step

	var err error
	switch ver {
	case structversion2:
		infoVer2 := info.ver2()
		err = binary.Write(pfi, binary.LittleEndian, &infoVer2)
	case structversion1:
		infoVer1 := info.ver1()
		err = binary.Write(pfi, binary.LittleEndian, &infoVer1)
	default:
		return Error.E(op, err, errPartialFileVersionTagUnsupported, 0, "")
	}

	if err != nil {
		return Error.E(op, err, errPartialFileWritingError, 0, "")
	}
	return nil
}

// GetJournalFileVersion returns version of a journal file.
// wp current pointer moved forward by uint32 bytes.
func GetJournalFileVersion(wp io.Reader) (uint32, error) {
	const op = "fsdriver.GetJournalFileVersion()"
	var ret uint32
	// what is the version of log file?

	err := binary.Read(wp, binary.LittleEndian, &ret)
	if err == io.EOF {
		// empty file. No error.
		return supportsLatestVer, nil //uses last supported by this fsdriver version
	}
	if err != nil {
		return 0, Error.E(op, err, errPartialFileReadingError, 0, "")
	}
	if ret < structversion1 || ret > supportsLatestVer {
		return 0, Error.E(op, err, errPartialFileVersionTagUnsupported, 0, "")
	}
	return ret, nil // supported version
}

// MayUpload decides if this file may be appended.
// It looks for correspondent journal file for this file,
// and use journal to get a file state of the file being uploaded.
// Upload allowed when there is no such file OR when such file exists and has correspondent journal file.
// MayUpload analize journal file for current state.
func MayUpload(storagepath string, name string) (FileState, error) {
	const op = "fsdriver.MayUpload()"
	namepart := GetPartialJournalFileName(name)

	wastat, err := os.Stat(filepath.Join(storagepath, name))
	if os.IsNotExist(err) {
		// no actual file, client may upload
		return *NewFileState(0, nil, 0), nil
	}
	if err != nil {
		// can't get stat for actual file. Who knows why, error.
		return *NewFileState(0, nil, 0),
			Error.E(op, err, errForbidenToUpdateAFile, Error.ErrKindInfoForUsers, "")
	}

	// next read journal file
	wp, err := openToRead(storagepath, namepart)
	if err != nil {
		// err != nil means no log file exists or read error.
		// These states do not allow file change.
		return *NewFileState(wastat.Size(), nil, wastat.Size()),
			Error.E(op, err, errForbidenToUpdateAFile, 0, "")
	}
	defer wp.Close()

	ver, err := GetJournalFileVersion(wp)
	if err != nil { // unsupported version or read error
		return *NewFileState(0, nil, 0),
			Error.E(op, err, errForbidenToUpdateAFile, 0, "")
	}

	var fromLog FileState
	var journaloffset int64 // journal file position
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
		return *NewFileState(0, nil, 0), Error.E(op, err, errPartialFileVersionTagUnsupported, 0, "")
	}

	if errlog != nil {
		// we need Code field in error
		errlogError, _ := errlog.(*Error.Error)
		if errlogError.Code == errPartialFileReadingError {
			// Here errlog indicates we can not read journal file.
			// can't do anything with journal file, even reading.
			return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.FileSize),
				errlog
		}

		// Here errlog is a logical error in journal file.
		// log(journal) file needs repair.
		// A simple case is when actual file size == fromLog.startoffset.
		// It is when an upload did not complete.
		if fromLog.FileSize != 0 && // log at least has filesize
			fromLog.Startoffset == wastat.Size() && // offset is equal to actual file size
			fromLog.FileSize > fromLog.Startoffset { // maybe half of a file have been downloaded

			// May be that uploadserver was shutted down in the process of writing to the journal file.
			// try to repair journal file to lcontinue upload
			if err := wp.Truncate(journaloffset); err == nil {
				// Here the journal is repaired.
				// One may continue to upload the actual file.
				return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset), nil
			}
		}

		// here journal file is considdered to be in corrupted state
		return *NewFileState(wastat.Size(), fromLog.Sha1, wastat.Size()),
			Error.E(op, err, errPartialFileCorrupted, 0, "") // error in log file blocks updates to actual file
	}
	// Here the journal file has been read with no error.
	// Here struct fromLog has last correct offset of the actual file being uploaded, fromLog.Startoffset.
	// Lets consider the journal file fromLog.FileSize a correct expected actual file size.

	if wastat.Size() > fromLog.FileSize {
		// actual file already bigger then expected!
		// Impossible unless a user has intervened or journal file is bad.
		log.Printf("Аctual file already bigger then expected: %s has %d bytes, journal says %d bytes.", name, wastat.Size(), fromLog.Startoffset)
		return *NewFileState(wastat.Size(), fromLog.Sha1, fromLog.Startoffset),
			Error.E(op, err, errActualFileAlreadyBiggerThanExpacted, 0, "")
	}

	if wastat.Size() == fromLog.FileSize {

		if fromLog.Startoffset == fromLog.FileSize {
			// The actual file is correct and completly uploaded, but partial journal file still exists.
			// Return an error as a special indication of this inconsistency, no need to allow further upload.
			return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset),
				Error.E(op, err, errActualFileIsAlreadyCompleteButJournalFileExists, 0, "")
		}
		return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset),
			Error.E(op, err, errActualFileIsAlreadyCompleteButJournalFileIsInconsistent, 0, "")

	}
	// CASE wastat.Size() < fromLog.FileSize
	if wastat.Size() == fromLog.Startoffset {
		// Upload may continue, current actual file size corresponds to saved offset in journal file.
		// Return no error.
		return *NewFileState(fromLog.FileSize, fromLog.Sha1, fromLog.Startoffset),
			nil
	}
	// Otherwise we need to read content of actual file and
	// compare its blocks's MD5 with MD5 sums of every block in journal file
	// to find the maximum correct range in actual file.

	// TODO(zavla): run thorough content compare with MD5 cheksums of blocks
	return *NewFileState(wastat.Size(), fromLog.Sha1, fromLog.Startoffset),
		Error.E(op, err, errActualFileNeedsRepare, 0, "")
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

// DecodePartialFile is used to get printable representation of a journal(log) file content.
// It is used by decodejournal package.
func DecodePartialFile(r io.Reader, w io.Writer) error {
	ver, err := GetJournalFileVersion(r)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "File has a header with version: %x\n", ver)
	switch ver {
	case structversion2:
		err = decodePartialFileVer2(r, w)
	case structversion1:
		err = decodePartialFileVer2(r, w)
	}
	return err

}
func decodePartialFileVer1(r io.Reader, w io.Writer) error {
	return nil
}
func decodePartialFileVer2(r io.Reader, w io.Writer) error {
	startstruct := startstructver2{}
	record := journalrecordver2{}

	err := binary.Read(r, binary.LittleEndian, &startstruct)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%#v\n", startstruct)

	for {
		err := binary.Read(r, binary.LittleEndian, &record)
		if err == io.EOF {
			return nil
		}
		if err == io.ErrUnexpectedEOF {
			// there are some bytes
			b := make([]byte, binary.Size(record))
			r.Read(b)
			fmt.Fprintf(w, "%x\n", b)
			return err
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%#v\n", record)

	}

}
