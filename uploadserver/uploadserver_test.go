package uploadserver

import (
	"os"
	"path/filepath"
	"testing"
	"upload/fsdriver"
)

func getbytes(value byte, size int) []byte {
	b := make([]byte, size)
	for i := 0; i < size; i++ {
		b[i] = value
	}
	return b
}

type args struct {
	chSource     chan []byte
	chResult     chan writeresult
	storagepath  string
	name         string
	destination  fsdriver.JournalRecord
	funcproducer func(ch chan []byte)
	expectcount  int64
}
type test struct {
	name string
	args args
}

const storagepath = "./testdata"

func Test_consumeSourceChannel(t *testing.T) {

	tests := []test{}

	// case 1
	producer := func(ch chan []byte) {
		ch <- getbytes(1, 100)
		ch <- getbytes(2, 200)
		ch <- getbytes(3, 300)
		close(ch)
	}
	addTest("case1", &tests, producer, 600)
	// end case
	// case 2
	producer2 := func(ch chan []byte) {
		ch <- getbytes(1, 100)
		ch <- getbytes(2, 200)
		ch <- getbytes(3, 65536)
		close(ch)
	}
	addTest("case2", &tests, producer2, 100+200+65536)
	// end case

	// RUN tests

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journalname := fsdriver.GetPartialJournalFileName(tt.args.name)
			os.Remove(filepath.Join(tt.args.storagepath, journalname))
			os.Remove(filepath.Join(tt.args.storagepath, tt.args.name))

			// fsdriver.OpenTwoCorrespondentFiles(tt.args.storagepath, tt.args.name, journalname)
			go consumeSourceChannel(tt.args.chSource, tt.args.chResult, tt.args.storagepath, tt.args.name, tt.args.destination)
			go tt.args.funcproducer(tt.args.chSource)
			get, ok := waitForWriteToFinish(tt.args.chResult, tt.args.expectcount)
			if !ok {
				t.Errorf("get ok==false, want ok==true, get.err==%s", get.err)
				return
			}
			if get.err != nil {
				t.Errorf("want writeresult.err==nil, get==%s", get.err)
			}

		})
	}
}

func addTest(testname string, tests *[]test, producer func(ch chan []byte), expectcount int64) {

	chReciever := make(chan []byte, 2) // allocate new channel
	chResult := make(chan writeresult)
	*tests = append(*tests, test{
		name: testname,
		args: args{
			chSource:     chReciever, // test will use allocated channel
			chResult:     chResult,
			storagepath:  storagepath,
			name:         testname,
			destination:  fsdriver.JournalRecord{Startoffset: 0},
			funcproducer: producer,
			expectcount:  expectcount,
		},
	})
	return
}

func Test_validatefilepath(t *testing.T) {
	type args struct {
		pathstr string
		maxlen  int
	}
	const m = 256
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
		{"1", args{"/filename.ext", m}, false},
		{"1", args{`\.filename.ext`, m}, false},
		{"1", args{`.filename.ext`, m}, false},
		{"1", args{`c:\.filename.ext`, m}, true},
		{"1", args{`\\.\c\.filename.ext`, m}, true},
		{"1", args{`\\?\c/filename.ext`, m}, true},
		{"1", args{`\c\.f i l e name.ext`, m}, false},
		{"1", args{`\c\long path$\f i l e name.ext`, m}, false},
		{"1", args{`long path$\..\f i l e name.ext`, m}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatefilepath(tt.args.pathstr, tt.args.maxlen); (err != nil) != tt.wantErr {
				t.Errorf("validatefilepath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
