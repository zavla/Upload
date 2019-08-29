package fsdriver

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	testdata = "./testdata"
)

type args struct {
	wp io.Reader
}

type dcase struct { // used in two funcs
	name         string
	args         args
	wantRetState FileState
	wantJOff     int64
	wantErr      bool
	errkind      error
	wantVer      uint32
}
type towrite struct {
	dcase
	rec bytes.Buffer // this is content of the test log file, not including startstruct1
}

func TestReadCurrentStateFromPartialFile(t *testing.T) {
	// here we create files
	datainfiles, err := createtestdata(t)
	// datainfiles holds files data and expected return from ReadCurrentStateFromPartialFile
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	// first, test with empty log file
	log1, err := os.Open("./testdata/log1.partialinfo")
	if err != nil {
		t.Errorf("log file with test data read error = %s", err)
		return
	}
	tests := []dcase{
		// case for empty file
		{name: "empty log file",
			args:         args{log1}, // here goes io.Reader to read from
			wantRetState: FileState{Startoffset: 0, FileProperties: FileProperties{FileSize: 0}},
			wantErr:      false,
			errkind:      nil,
			wantVer:      0,
		},
	}
	// next a first! recieved with panic file
	log2, err := os.Open("./testdata/checklater/sendfile.rar.partialinfo")
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	tests = append(tests, dcase{
		name:         "recived with panic",
		args:         args{log2}, // here goes io.Reader to read from
		wantRetState: FileState{Startoffset: 94207, FileProperties: FileProperties{FileSize: 16961536}},
		wantJOff:     0xDC + 4,
		wantErr:      true,
		errkind:      errPartialFileCorrupted,
		wantVer:      structversion2,
	})

	// next transform towrite structs to dcase structs
	for k, v := range datainfiles {
		// other test cases
		f := new(os.File)
		f, err := os.Open(filepath.Join(testdata, v.name))
		if err != nil {
			t.Errorf("%s", err)
			return
		}
		tests = append(tests, dcase{name: k,
			args:         args{f},
			wantRetState: v.wantRetState,
			wantJOff:     v.wantJOff,
			wantErr:      v.wantErr,
			errkind:      v.errkind,
			wantVer:      v.wantVer,
		})
	}

	// here goes checks
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, err := GetLogFileVersion(tt.args.wp)
			if err != nil {
				t.Errorf("version reading error %s", err)
				return
			}
			if ver != tt.wantVer {
				t.Errorf("version error, want = %x, got = %x", tt.wantVer, ver)
				return
			}
			var gotRetState FileState
			var offsetinjournal int64

			switch ver {
			case structversion1:
				gotRetState, offsetinjournal, err = ReadCurrentStateFromPartialFile_structversion1(tt.args.wp)

			case structversion2:
				gotRetState, offsetinjournal, err = ReadCurrentStateFromPartialFile_structversion2(tt.args.wp)
			default:
				t.Errorf("unexpected ver in file %x", ver)
				return
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadCurrentStateFromPartialFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != tt.errkind {
				t.Errorf("ReadCurrentStateFromPartialFile() = %v, want %v", err, tt.errkind)
				return
			}
			if !reflect.DeepEqual(gotRetState, tt.wantRetState) {
				t.Errorf("ReadCurrentStateFromPartialFile() = %#v, want %#v", gotRetState, tt.wantRetState)
			}
			if offsetinjournal != tt.wantJOff {
				t.Errorf("ReadCurrentStateFromPartialFile().wantJOff, got %d, want %d", offsetinjournal, tt.wantJOff)
			}
		})
	}
}
func tobytes(r ...interface{}) bytes.Buffer {
	ret := new(bytes.Buffer)
	for _, v := range r {
		binary.Write(ret, binary.LittleEndian, v)
	}
	return *ret
}
func createtestdata(t *testing.T) (map[string]towrite, error) {
	//----------------------- Add here cases
	// data is a slice to hold a test case "dcase" and file content "rec"
	// Every file will start with startstruct
	startlen := int64(binary.Size(StartStruct{}) + binary.Size(uint32(structversion1)))
	recordlen := int64(binary.Size(PartialFileInfo{}))

	data := []towrite{
		towrite{
			dcase: dcase{name: "onlystart",
				wantRetState: FileState{Startoffset: 0, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 0,
				wantErr:      false,
				errkind:      nil,
			},
			rec: bytes.Buffer{}}, // here no bytes in file
		towrite{
			dcase: dcase{name: "onlyfirstofpair",
				// last successful record points to offset 0
				wantRetState: FileState{Startoffset: 0, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 0,
				wantErr:      true,
				errkind:      errPartialFileCorrupted,
			},
			rec: tobytes(PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 1000}),
		},
		towrite{
			dcase: dcase{name: "onlyfirstofpairPlusGarbage",
				wantRetState: FileState{Startoffset: 0, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 0,

				wantErr: true,
				errkind: errPartialFileCorrupted, // file ended unexpectedly? use last success record
			},
			rec: tobytes(PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 1000},
				[]byte{01, 02, 03}),
		},
		towrite{
			dcase: dcase{name: "fullPair1000",
				wantRetState: FileState{Startoffset: 1000, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 2*recordlen,

				wantErr: false,
				errkind: nil, // file uploaded fully with one write
			},
			rec: tobytes(PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 1000},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 1000}),
		},
		towrite{
			dcase: dcase{name: "inPairFirstOffsetIsBigger",
				wantRetState: FileState{Startoffset: 0, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 0,

				wantErr: true,
				errkind: errPartialFileCorrupted, // there is an error, expects last successful record
			},
			rec: tobytes(PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 1000},
				PartialFileInfo{Action: successwriting, Startoffset: 10, Count: 1000}),
		},
		towrite{
			dcase: dcase{name: "ManyPairsinPairFirstOffsetIsBigger",
				wantRetState: FileState{Startoffset: 500, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 2*recordlen,

				wantErr: true,
				errkind: errPartialFileCorrupted, // there is an error, expects last successful record
			},
			rec: tobytes(
				PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 501, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 500, Count: 500}),
		},
		towrite{
			dcase: dcase{name: "ManyPairsinLastPairIncomplete",
				wantRetState: FileState{Startoffset: 500, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 2*recordlen,

				wantErr: true,
				errkind: errPartialFileCorrupted, // corrupt
			},
			rec: tobytes(
				PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 501, Count: 500},
			),
		},
		towrite{
			dcase: dcase{name: "ManyPairsWrongAction",
				wantRetState: FileState{Startoffset: 500, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 2*recordlen,

				wantErr: true,
				errkind: errPartialFileCorrupted, // there is an error, action is wrong
			},
			rec: tobytes(
				PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 501, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 501, Count: 500},
			),
		},
		towrite{
			dcase: dcase{name: "ManyPairsNotAllowedAction",
				wantRetState: FileState{Startoffset: 500, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 2*recordlen,

				wantErr: true,
				errkind: errPartialFileCorrupted, // there is an error, Action is wrong
			},
			rec: tobytes(
				PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: CurrentAction(99), Startoffset: 501, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 501, Count: 500},
			),
		},
		towrite{
			dcase: dcase{name: "TwoPairsLastIsWrong",
				wantRetState: FileState{Startoffset: 500, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 2*recordlen,

				wantErr: true,
				errkind: errPartialFileCorrupted, // there is an error, action is wrong
			},
			rec: tobytes(
				PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 500, Count: 499},
				PartialFileInfo{Action: startedwriting, Startoffset: 500, Count: 499},
			),
		},
		towrite{
			dcase: dcase{name: "TwoPairsAllComplete",
				wantRetState: FileState{Startoffset: 1000, FileProperties: FileProperties{FileSize: 1000}},
				wantJOff:     startlen + 4*recordlen,

				wantErr: false,
				errkind: nil, // no error
			},
			rec: tobytes(
				PartialFileInfo{Action: startedwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 0, Count: 500},
				PartialFileInfo{Action: startedwriting, Startoffset: 500, Count: 500},
				PartialFileInfo{Action: successwriting, Startoffset: 500, Count: 500},
			),
		},
	}
	//-----------------------
	// everywhere is structversion1
	for k, _ := range data {
		data[k].wantVer = structversion1
	}
	//
	ret := make(map[string]towrite) // a return, map[filename]towrite

	// next is a start header of a log file
	startrecord := StartStruct{VersionBytes: structversion1,
		VersionBytesEnd:         structversion1,
		TotalExpectedFileLength: 1000}

	// loop writes to test files
	for _, addtofile := range data {

		f, err := os.OpenFile(filepath.Join(testdata, addtofile.name), os.O_CREATE|os.O_TRUNC, 0)
		if err != nil {
			t.Errorf("%s", err)
			return ret, err
		}
		binary.Write(f, binary.LittleEndian, uint32(structversion1)) // writes version
		binary.Write(f, binary.LittleEndian, startrecord)            // writes header

		{ // writes all the rest bytes
			binary.Write(f, binary.LittleEndian, addtofile.rec.Bytes())
		}
		f.Close()

		ret[addtofile.name] = addtofile
	}
	return ret, nil
}
