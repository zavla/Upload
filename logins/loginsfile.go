package logins

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
	Error "upload/errstr" // we depend on new standard functions: Unwrap, Is, As
	"upload/httpDigestAuthentication"

	"golang.org/x/crypto/ssh/terminal"
)

type Manager interface {
	Save() error
	Add(login string, email string, password string) (Login, error)
	Find(login string, lockX bool) (*Login, int, error)
	OpenDB(path string) error
}

const ver1 = "1"

var Store Logins // type Logins uses interface 'manager'

// Login represents a user
type Login struct {
	Login        string `json:"id"` // unique id
	Email        string `json:"email"`
	Passwordhash string //md5hex("%s:%s:%s", username,realm,password)
	Disabled     bool   `json:"disabled"`
	mu           *sync.Mutex
}
type Logins struct {
	Version      string  `json:"version"`
	Logins       []Login `json:"logins"` // goes to disk
	sortedLogins []*Login
	filename     string
}

func (ls *Logins) Find(login string, lockForUpdate bool) (*Login, int, error) {
	const op = "logins.Find()"
	firstGEFunc := func(i int) bool {
		if ls.sortedLogins[i].Login >= login {
			return true
		}
		return false
	}
	pos := sort.Search(len(ls.sortedLogins), firstGEFunc)
	if pos < len(ls.sortedLogins) && ls.sortedLogins[pos].Login == login {
		if lockForUpdate {
			ls.sortedLogins[pos].mu.Lock() // exclusive lock, prepare to update
		}
		return ls.sortedLogins[pos], pos, nil
	}
	return nil, -1, os.ErrNotExist
}
func (ls *Logins) updateSortedlogins() {
	ls.sortedLogins = make([]*Login, len(ls.Logins))
	for i := 0; i < len(ls.sortedLogins); i++ {
		ls.sortedLogins[i] = &ls.Logins[i]
	}
}
func (ls *Logins) Add(login string, email string, password string) (Login, error) {
	newhash := httpDigestAuthentication.HashUsernameRealmPassword(login, "upload", password)
	l, _, err := ls.Find(login, true) // locks mu
	if os.IsNotExist(err) {
		// l is on stack
		l := Login{
			Login:        login,
			Email:        email,
			Passwordhash: newhash,
			Disabled:     false,
			mu:           new(sync.Mutex),
		}
		oldcap := cap(ls.Logins)
		ls.Logins = append(ls.Logins, l) // l is moved to the heap
		newcap := cap(ls.Logins)
		if oldcap == newcap { // was no relocation
			// TODO(zavla): quick exist/not exist a login; quick move from login to its actual struct.
			ls.sortedLogins = append(ls.sortedLogins, &ls.Logins[len(ls.Logins)-1]) // address of last element
		} else {
			// ls.Logins has been relocated, no pointers remains valid.
			ls.updateSortedlogins()
		}
		ls.sortPointersToLogins("login")

		return l, nil
	}
	l.Passwordhash = newhash
	l.Email = email
	l.mu.Unlock()
	return *l, nil

}

func (ls *Logins) Save() error {
	return ls.writeLoginsJSON()
}

// sortPointersToLogins sorts logins in slice, allows binary search.
func (ls *Logins) sortPointersToLogins(field string) {
	if field == "login" {
		sort.Slice(ls.sortedLogins, func(i, j int) bool {
			if ls.sortedLogins[i].Login < ls.sortedLogins[j].Login {
				return true
			}
			return false
		})
		return
	}
	if field == "email" {
		sort.Slice(ls.sortedLogins, func(i int, j int) bool {
			if ls.sortedLogins[i].Email < ls.sortedLogins[j].Email {
				return true
			}
			return false
		})
	}

}

func (ls *Logins) OpenDB(path string) error {
	newls, err := ReadLoginsJSON(path)
	if err != nil {
		return err
	}
	*ls = newls
	return nil
}

func AskAndSavePasswordForHTTPDigest(loginsmanager Manager, loginobj Login, realm string) error {
	const op = "logins.AskAndSavePasswordForHTTPDigest()"
	fmt.Printf("\nEnter user '%s' password: ", loginobj.Login)
	password, err := terminal.ReadPassword(int(os.Stdin.Fd()))

	fmt.Println("")
	if err != nil {
		return Error.E(op, err, errReadPassword, 0, "")
	}
	hashUsernameRealmPassword := httpDigestAuthentication.HashUsernameRealmPassword(loginobj.Login, realm, string(password))
	_, err = loginsmanager.Add(loginobj.Login, "", hashUsernameRealmPassword)
	if err != nil {
		return Error.E(op, err, errLoginsManagerCantAdd, 0, "")
	}
	err = loginsmanager.Save()
	if err != nil {
		return Error.E(op, err, errLoginsManagerCantSave, 0, "")

	}
	return nil

}

// ReadLoginsJSON from file. Remember to call sortLogins after this.
func ReadLoginsJSON(filename string) (Logins, error) {
	b, err := ioutil.ReadFile(filename)
	ls := Logins{Version: ver1,
		Logins:       make([]Login, 0),
		filename:     filename,
		sortedLogins: make([]*Login, 0),
	}
	if err != nil {
		if os.IsNotExist(err) {
			return ls, nil // not existent file will be created on Write
		}
		return ls, err // read error is serious
	}

	err = json.Unmarshal(b, &ls)
	if err != nil {
		return ls, err // serious
	}
	//
	for i := range ls.Logins {
		ls.Logins[i].mu = new(sync.Mutex)
	}
	ls.updateSortedlogins()
	ls.sortPointersToLogins("login")

	return ls, nil
}

// func NewLoginsFile(filename string) error {
// 	// f, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0)
// 	// if err != nil {
// 	// 	return err
// 	// }
// 	ls := Logins{
// 		Version: ver1,
// 		Logins:  make([]Login, 0),
// 	}
// 	return WriteLoginsJSON(filename, ls)

// }

// WriteLoginsJSON saves logins into JSON file. All at once.
func (ls *Logins) writeLoginsJSON() error {
	const op = "logins.WriteLoginsJSON()"
	dir, _ := filepath.Split(ls.filename)
	ftmp, err := ioutil.TempFile(dir, "logins*")
	if err != nil {
		return Error.E(op, err, errTempFileCreationFailed, 0, "")
	}

	b, err := json.MarshalIndent(*ls, " ", " ")
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
	err = os.Rename(ftmp.Name(), ls.filename)
	if err != nil {
		return Error.E(op, err, Error.ErrFileIO, 0, "")
	}
	return nil
}

// // SetAndStorePassword stores password hash using function updateLoginInStore.
// func (ls *Logins) SetAndStorePassword(email, bhash string) (Login, error) {
// 	const op = "logins.SetAndStorePassword"

// 	l, pos, err := ls.GetByEmail(email, true) // Xlock
// 	if err != nil {
// 		return Login{}, Error.E(op, nil, errLoginNotFound, 0, "")
// 	}
// 	// here ls[pos].mu is locked
// 	defer ls.Logins[pos].mu.Unlock() // unlockit

// 	// next persist it to disk or somehow store
// 	dupLogin := l
// 	// we made a copy
// 	err = updateLoginInStore(dupLogin)
// 	if err != nil {
// 		return Login{}, Error.E(op, err, errSaveLogin, 0, "")
// 	}
// 	l.Passwordhash = bhash // updates inmemmory logins with bhash

// 	return l, nil
// }
// func packLoginIntoBlock(l Login) (ondiskpage, error) {
// 	const op = "logins.packLoginIntoBlock"
// 	b, err := json.MarshalIndent(l, "", " ") // space as an indent
// 	if err != nil {
// 		return ondiskpage{}, Error.E(op, err, errEncodingDecoding, 0, "")
// 	}
// 	ret := ondiskpage{}
// 	copy(ret.page[:], b)
// 	return ret, nil
// }

// // updateLoginInStore adds new block to the end of a file
// func updateLoginInStore(new Login) error {

// 	const op = "logins.updateLoginInStore"

// 	byteblock, err := packLoginIntoBlock(new)
// 	if err != nil {
// 		return Error.E(op, err, errPackIntoBlockFailed, 0, "")
// 	}
// 	// adds to the end
// 	f, err := os.OpenFile(loginsFile, os.O_APPEND, 0)
// 	if err != nil {
// 		return Error.E(op, err, errFileOpen, 0, "")
// 	}
// 	defer f.Close() // delayed second close on purpose

// 	finfo, err := f.Stat()
// 	if err != nil {
// 		return Error.E(op, err, int16(Error.ErrFileIO), 0, "")
// 	}
// 	fsize := finfo.Size()
// 	// align file length to pagesize if needed
// 	currentAling := int(fsize & pagesize)
// 	needToAdd := pagesize - currentAling
// 	// write block
// 	err = nil
// 	written := 0
// 	if needToAdd != 0 {
// 		tempb := bytes.Repeat([]byte{0x20}, needToAdd+pagesize)
// 		copy(tempb, byteblock.page[:])
// 		written, err = f.Write(tempb)

// 	} else {
// 		written, err = f.Write(byteblock.page[:])
// 	}
// 	log.Printf("Block with login %s, written %d bytes.", new.Email, written)
// 	err = f.Close()
// 	if err != nil {
// 		return Error.E(op, err, Error.ErrFileIO, 0, "")
// 	}
// 	return nil

// }

// GetByField relies on sorted by this "field" []Login.
// Returns a position of an element in the slice ls.
// Example: firstBiggerFunc = func(i int) bool {
// 	if ls.Logins[i].Email < email {
// 		return false
// 	}
// 	return true
// }
// Example: getter = func(obj *Login) *string {
//	return obj.Email
//}
// ondiskpage is a block with a Login on disk
// type ondiskpage struct {
// 	page [pagesize]byte
// }

// func (p *ondiskpage) clear() {
// 	copy(p.page[:], bytes.Repeat([]byte{0x20}, pagesize))
// }
// const pagesize = 255
