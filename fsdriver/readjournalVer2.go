package fsdriver

import (
	"encoding/binary"
	"io"
	Error "upload/errstr"
)

// startstructver2 is a header of version 2 journal
type startstructver2 struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	Sha1                    [20]byte
	VersionBytesEnd         uint32
}

// universal StartStruct -> to StartStructVer2
func (s startstruct) Ver2() startstructver2 {
	return startstructver2{
		VersionBytes:            s.VersionBytes,
		VersionBytesEnd:         s.VersionBytesEnd,
		TotalExpectedFileLength: s.TotalExpectedFileLength,
		Sha1:                    s.Sha1,
	}

}

// journalrecordver2 this is a record version 2
type journalrecordver2 struct {
	Action      currentAction
	Startoffset int64
	Count       int64
	Crc32       int32
}

// ver2 translates JournalRecord to old version 2
func (r JournalRecord) ver2() journalrecordver2 {
	return journalrecordver2{
		Action:      r.Action,
		Startoffset: r.Startoffset,
		Count:       r.Count,
		Crc32:       r.Crc32,
	}
}

// ReadCurrentStateFromJournalVer2 reads special version of journal file.
// It returns last correct log record and an error (either reading error or logical error).
// It checks correctness of the log(journal) and may return a logical error.
// Every format version of a journal file uses its own such function.
func ReadCurrentStateFromJournalVer2(ver uint32, wp io.Reader) (retState FileState, correctrecordoffset int64, errInLog error) {
	const op = "fsdriver.ReadCurrentStateFromJournalVer2()"
	var startstruct startstructver2
	var headersize = int64(binary.Size(startstructver2{})) //depends on header version
	var recordsize = int64(binary.Size(journalrecordver2{}))

	// !nil means the log-journal file must be truncated at last correct record to continue usage of this journal file
	errInLog = nil

	// correctrecordoffset points to the end of the last correct record
	correctrecordoffset = int64(binary.Size(structversion2)) // holds journal file offset

	retState = *NewFileState(0, nil, 0)

	// reads start bytes in file
	err := binary.Read(wp, binary.LittleEndian, &startstruct)
	if err != nil {
		if err == io.EOF {
			// empty file. No error.
			return retState,
				0,
				nil
		}
		// No startstruct in header. error. File created but somehow without a startstruct header.
		return retState,
			correctrecordoffset,
			Error.E(op, err, errPartialFileVersionTagReadError, 0, "")
	}

	// Here there is a header.
	// Check the header for correctness.
	if startstruct.VersionBytes == structversion2 && startstruct.VersionBytesEnd == structversion2 {

		correctrecordoffset += headersize
		retState.FileSize = startstruct.TotalExpectedFileLength
		retState.Sha1 = startstruct.Sha1[:]

		currrecord := journalrecordver2{} // current log record

		// initial value for prevrecord wil never be a match to currrecord
		prevrecord := journalrecordver2{
			Startoffset: -1,
			Action:      successwriting} // forced to do not make a match

		lastsuccessrecord := journalrecordver2{} // should not be a pointer
		numrecords := int64(0)
		lasterr := error(nil)
		maybeErr := false

		for { // reading records one by one
			err := binary.Read(wp, binary.LittleEndian, &currrecord)
			// next lines are dealing with bad errors while reading. But EOF is special, it means we succeded is reading.
			if err == io.ErrUnexpectedEOF { // read ended unexpectedly
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					Error.E(op, err, errPartialFileCorrupted, 0, "")
			} else if err != nil && err != io.EOF { // read failed, use lastsuccessrecord
				//returns previous good record
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					Error.E(op, err, errPartialFileReadingError, 0, "") // we dont know why we cant read journal. may be its ok?.
			}

			// make sure offsets come in ascending order
			// next lines are dealing with offsets in current record, decides if this record is good
			switch {
			case currrecord.Startoffset > retState.FileSize:
				// current record do not corresond to expected file size
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					Error.E(op, err, errPartialFileCorrupted, 0, "Startoffset in journal size already exceeded expected file size.")
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
					// Journal file doesn't have a 'write ended' record.
					lasterr = Error.E(op, err, errPartialFileCorrupted, 0, "")
				}
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					lasterr // at EOF it depends on previous record if this is corruption or no.
			default:
				// current record is bad.
				return *retState.Setoffset(lastsuccessrecord.Startoffset + lastsuccessrecord.Count),
					correctrecordoffset,
					Error.E(op, err, errPartialFileCorrupted, 0, "")
			} // end switch
			prevrecord = currrecord // makes previous record a current one
			// continue to next record
		}
	} else {
		// incorrect header
		return retState, correctrecordoffset, Error.E(op, err, errPartialFileVersionTagReadError, 0, "")
		//.SetDetails("has version = %x", startstruct.VersionBytes)
	}

}
