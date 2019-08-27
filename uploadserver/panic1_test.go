package uploadserver

import (
	"Upload/fsdriver"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPanic1(t *testing.T) {
	// Create recieve channel to write bytes for this connection.
	// chReciever expects bytes from io.Reader to be written to file.
	chReciever := make(chan []byte, 2)
	// chWriteResult is used to send result of WriteChanneltoDisk goroutine.
	chWriteResult := make(chan writeresult)
	testdatafile := "../fsdriver/testdata/checklater/sendfile.rar"
	testfilename := filepath.Clean(testdatafile)
	f, err := os.Open(testfilename)
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	storagepath := "../fsdriver/testdata/checklater/panic1"
	name := "sendfile.rar"
	// clear panic1 dir
	if temp, err := os.Open(filepath.Join(storagepath, name)); err == nil {
		temp.Close()
		err = os.Remove(filepath.Join(storagepath, name))
		if err != nil {
			t.Errorf("%s", err)
			return
		}
	}
	journalname := filepath.Join(storagepath, fsdriver.CreatePartialFileName(name))
	//if _, err = os.Open(journalname); err == nil {
	_ = os.Remove(journalname)
	// if err != nil {
	// 	t.Errorf("%s", err)
	// 	return
	// }
	//}
	errreciver, writeresult := RecieveAndWriteAndWait(f,
		chReciever,
		chWriteResult,
		storagepath,
		name,
		fsdriver.PartialFileInfo{Startoffset: 0, Count: 200000})

	if errreciver != nil || writeresult.err != nil {
		// we expect a panic in fsdriver.AddBytesToFileInHunks
		t.Logf("writeresult.err == %s", writeresult.err)
		fmt.Println("got err", " ", writeresult.err)

	} else {
		t.Errorf("want panic in fsdriver.AddBytesToFileInHunks")
		return
	}

}
