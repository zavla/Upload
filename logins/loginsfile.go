package logins

import (
	Error "Upload/errstr" // we depend on new standard functions: Unwrap, Is, As
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	loginsFile string
)

// Login represents a user
type Login struct {
	Login string `json:"id"` // unique id
	Email string `json:"email"`
	//Passwordhash [60]byte `json:"passwordhash"` // looks like [60]byte is a standatd bcrypt header+hash+salt
	Passwordhash string //md5hex("%s:%s:%s", username,realm,password)
	Disabled     bool   `json:"disabled"`
	mu           *sync.Mutex
}

const pagesize = 255

// ondiskpage is a block with a Login on disk
type ondiskpage struct {
	page [pagesize]byte
}

func (p *ondiskpage) clear() {
	copy(p.page[:], bytes.Repeat([]byte{0x20}, pagesize))
}

// Logins is a [] loaded from db
type Logins struct {
	Version string  `json:"version"`
	Logins  []Login `json:"logins"` // keep them sorted in memory
}

// SetAndStorePassword stores password hash using function updateLoginInStore.
func (ls *Logins) SetAndStorePassword(email, bhash string) (Login, error) {
	const op = "logins.SetAndStorePassword"

	l, pos, err := ls.GetByEmail(email, true) // Xlock
	if err != nil {
		return Login{}, Error.E(op, nil, errLoginNotFound, 0, "")
	}
	// here ls[pos].mu is locked
	defer ls.Logins[pos].mu.Unlock() // unlockit

	// next persist it to disk or somehow store
	dupLogin := l
	// we made a copy
	err = updateLoginInStore(dupLogin)
	if err != nil {
		return Login{}, Error.E(op, err, errSaveLogin, 0, "")
	}
	l.Passwordhash = bhash // updates inmemmory logins with bhash

	return l, nil
}
func packLoginIntoBlock(l Login) (ondiskpage, error) {
	const op = "logins.packLoginIntoBlock"
	b, err := json.MarshalIndent(l, "", " ") // space as an indent
	if err != nil {
		return ondiskpage{}, Error.E(op, err, errEncodingDecoding, 0, "")
	}
	ret := ondiskpage{}
	copy(ret.page[:], b)
	return ret, nil
}

// updateLoginInStore adds new block to the end of a file
func updateLoginInStore(new Login) error {

	const op = "logins.updateLoginInStore"

	byteblock, err := packLoginIntoBlock(new)
	if err != nil {
		return Error.E(op, err, errPackIntoBlockFailed, 0, "")
	}
	// adds to the end
	f, err := os.OpenFile(loginsFile, os.O_APPEND, 0)
	if err != nil {
		return Error.E(op, err, errFileOpen, 0, "")
	}
	defer f.Close() // delayed second close on purpose

	finfo, err := f.Stat()
	if err != nil {
		return Error.E(op, err, int16(Error.ErrFileIO), 0, "")
	}
	fsize := finfo.Size()
	// align file length to pagesize if needed
	currentAling := int(fsize & pagesize)
	needToAdd := pagesize - currentAling
	// write block
	err = nil
	written := 0
	if needToAdd != 0 {
		tempb := bytes.Repeat([]byte{0x20}, needToAdd+pagesize)
		copy(tempb, byteblock.page[:])
		written, err = f.Write(tempb)

	} else {
		written, err = f.Write(byteblock.page[:])
	}
	log.Printf("Block with login %s, written %d bytes.", new.Email, written)
	err = f.Close()
	if err != nil {
		return Error.E(op, err, Error.ErrFileIO, 0, "")
	}
	return nil

}

// GetByEmail relies on sorted []Login.
// returns pos as position of a login in the ls
func (ls *Logins) GetByEmail(email string, lockForUpdate bool) (Login, int, error) {
	const op = "logins.GetByEmail()"

	pos := sort.Search(len(ls.Logins), func(i int) bool {
		if ls.Logins[i].Email < email {
			return false
		}
		return true
	})
	if pos < len(ls.Logins) && ls.Logins[pos].Email == email {
		if lockForUpdate {
			ls.Logins[pos].mu.Lock() // exclusive lock, prepare to update
		}
		return ls.Logins[pos], pos, nil
	}
	return Login{}, -1, Error.E(op, nil, errLoginNotFound, 0, "")
}

// ReadLoginsJSON from file. Remember to call sortLogins after this.
func ReadLoginsJSON(name string) (Logins, error) {
	b, err := ioutil.ReadFile(name)
	ls := Logins{}
	if err != nil {
		return ls, err
	}
	err = json.Unmarshal(b, &ls)
	if err != nil {
		return ls, err
	}

	return ls, nil
}

// SortLogins sorts logins in slice, allows binary search.
func SortLogins(ls []Login) {
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].Email < ls[j].Email {
			return true
		}
		return false
	})
}

// WriteLoginsJSON saves logins into JSON file. All at once.
func WriteLoginsJSON(name string, logins Logins) error {
	const op = "logins.WriteLoginsJSON()"
	dir, _ := filepath.Split(name)
	ftmp, err := ioutil.TempFile(dir, "logins*")
	if err != nil {
		return Error.E(op, err, errTempFileCreationFailed, 0, "")
	}

	b, err := json.MarshalIndent(logins, " ", " ")
	if err != nil {
		return Error.E(op, err, errEncodingDecoding, 0, "")
	}
	_, err = ftmp.Write(b)
	if err != nil {
		return Error.E(op, err, Error.ErrFileIO, 0, "")
	}
	ftmp.Close()
	if err != nil {
		return Error.E(op, err, Error.ErrFileIO, 0, "")
	}
	// rename original logins file
	err = os.Rename(ftmp.Name(), name)
	if err != nil {
		return Error.E(op, err, Error.ErrFileIO, 0, "")
	}
	return nil
}
