package main

import (
	"golang.org/x/sys/unix"
)

const constAttributeUploaded = "user.uploaded"

func markFileAsUploaded(fullfilename string) error {
	err := unix.Setxattr(fullfilename, constAttributeUploaded, []byte{}, 0)
	if err != nil {
		return err
	}
	return nil

}

func getArchiveAttribute(fullfilename string) (bool, error) {
	_, err := unix.Getxattr(fullfilename, constAttributeUploaded, nil)
	if err == nil {
		return true, nil
	} else if err == unix.ENODATA {
		// no attribute
	} else {
		// error reading attributes
		return false, err
	}

	return false, nil
}

// TODO(zavla): make use of encryption on linux
func encryptByOs(b []byte) ([]byte, error) {
	//encrBytes, err := dpapi.Encrypt(b)
	// if err != nil {
	// 	return nil, err
	// }
	return b, nil
}
func decryptByOs(b []byte) ([]byte, error) {
	//encrBytes, err := dpapi.Encrypt(b)
	// if err != nil {
	// 	return nil, err
	// }
	return b, nil
}
