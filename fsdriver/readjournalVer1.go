package fsdriver

import (
	Error "Upload/errstr"
	"encoding/binary"
	"io"
)

// startstructver1 is a header of version 1 of journal.
type startstructver1 struct {
	VersionBytes            uint32
	TotalExpectedFileLength int64
	VersionBytesEnd         uint32
}

// universal StartStruct -> to StartStructVer1
func (s startstruct) Ver1() startstructver1 {
	return startstructver1{
		VersionBytes:            s.VersionBytes,
		VersionBytesEnd:         s.VersionBytesEnd,
		TotalExpectedFileLength: s.TotalExpectedFileLength,
	}
}

// journalrecordver1 this is a journal record version 1
type journalrecordver1 struct {
	Action      currentAction
	Startoffset int64
	Count       int64
}

// ver1 translates JournalRecord to old version 1
func (r JournalRecord) ver1() journalrecordver1 {
	return journalrecordver1{
		Action:      r.Action,
		Startoffset: r.Startoffset,
		Count:       r.Count,
	}
}

// ReadCurrentStateFromJournalVer1 reads log(journal) file.
// It returns last correct log record.
// It checks correctness of the log(journal).
// Every format version of a journal file uses its own such function.
func ReadCurrentStateFromJournalVer1(ver uint32, wp io.Reader) (retState FileState, correctrecordoffset int64, errInLog error) {
	const op = "fsdriver.ReadCurrentStateFromJournalVer1()"
	var startstruct startstructver1
	var headersize = int64(binary.Size(startstructver1{})) //depends on header version
	var recordsize = int64(binary.Size(journalrecordver1{}))

	// !nil means the log-journal file must be truncated at last correct record to continue usage of this journal file
	errInLog = nil

	// correctrecordoffset points to the end of the last correct record
	correctrecordoffset = int64(binary.Size(structversion1)) // holds journal file offset

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
	if startstruct.VersionBytes == structversion1 && startstruct.VersionBytesEnd == structversion1 {
		correctrecordoffset += headersize
		retState.FileSize = startstruct.TotalExpectedFileLength

		currrecord := journalrecordver1{} // current log record

		// initial value for prevrecord wil never be a match to currrecord
		prevrecord := journalrecordver1{
			Startoffset: -1,
			Action:      successwriting} // forced to do not make a match

		lastsuccessrecord := journalrecordver1{} // should not be a pointer
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
					Error.E(op, err, errPartialFileCorrupted, 0, "")
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
	}
}
