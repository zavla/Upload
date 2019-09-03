package fsdriver

import (
	"encoding/binary"
	"fmt"
	"io"
)

// for future support of journal version change
// TODO(zavla): may be use protobuf to store journal records
func ReadCurrentStateFromPartialFileVer2(ver uint32, wp io.Reader) (retState FileState, correctrecordoffset int64, errInLog error) {

	var startstruct StartStructVer2
	var headersize = int64(binary.Size(StartStructVer2{})) //depends on header version
	var recordsize = int64(binary.Size(PartialFileInfoVer2{}))

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
			errPartialFileVersionTagReadError
	}

	// Here there is a header.
	// Check the header for correctness.
	if startstruct.VersionBytes == structversion2 && startstruct.VersionBytesEnd == structversion2 {
		correctrecordoffset += headersize
		retState.FileSize = startstruct.TotalExpectedFileLength

		currrecord := PartialFileInfoVer2{} // current log record

		// initial value for prevrecord wil never be a match to currrecord
		prevrecord := PartialFileInfoVer2{
			Startoffset: -1,
			Action:      successwriting} // forced to do not make a match

		lastsuccessrecord := PartialFileInfoVer2{} // should not be a pointer
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
		return retState, correctrecordoffset, errPartialFileVersionTagReadError.SetDetails("has version = %x", startstruct.VersionBytes)
	}

}

// ReadCurrentStateFromPartialFile reads log(journal) file.
// It returns last correct log record.
func ReadCurrentStateFromPartialFileVer1(ver uint32, wp io.Reader) (retState FileState, correctrecordoffset int64, errInLog error) {

	var startstruct StartStructVer1
	var headersize = int64(binary.Size(StartStructVer1{})) //depends on header version
	var recordsize = int64(binary.Size(PartialFileInfoVer1{}))

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
			errPartialFileVersionTagReadError
	}

	// Here there is a header.
	// Check the header for correctness.
	if startstruct.VersionBytes == structversion1 && startstruct.VersionBytesEnd == structversion1 {
		correctrecordoffset += headersize
		retState.FileSize = startstruct.TotalExpectedFileLength

		currrecord := PartialFileInfoVer1{} // current log record

		// initial value for prevrecord wil never be a match to currrecord
		prevrecord := PartialFileInfoVer1{
			Startoffset: -1,
			Action:      successwriting} // forced to do not make a match

		lastsuccessrecord := PartialFileInfoVer1{} // should not be a pointer
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

func DecodePartialFile(r io.Reader, w io.Writer) error {
	ver, err := GetLogFileVersion(r)
	fmt.Fprintf(w, "File version: %x\n", ver)
	if err != nil {
		return err
	}

	err = DecodePartialFileVer2(r, w)
	return err

}

func DecodePartialFileVer2(r io.Reader, w io.Writer) error {
	startstruct := StartStructVer2{}
	record := PartialFileInfoVer2{}

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
