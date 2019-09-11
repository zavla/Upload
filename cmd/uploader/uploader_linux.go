package main

import (
	"os"

	chattr "github.com/g0rbe/go-chattr"
)

func MarkFileAsUploaded(fullfilename string) error {
	f, err := os.Open(fullfilename)
	if err != nil {
		return err
	}
	defer f.Close()
	return chattr.SetAttr(f, chattr.FS_NODUMP_FL)

}

func GetArchiveAttribute(fullfilename string) (bool, error) {
	f, err := os.Open(fullfilename)
	if err != nil {
		return false, err
	}
	defer f.Close()

	return chattr.IsAttr(f, chattr.FS_NODUMP_FL)
}
