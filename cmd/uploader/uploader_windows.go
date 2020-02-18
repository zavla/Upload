package main

import (
	"log"

	"golang.org/x/sys/windows"
)

func markFileAsUploaded(fullfilename string) error {
	// uses Windows API
	ptrFilenameUint16, err := windows.UTF16PtrFromString(fullfilename)
	if err != nil {
		log.Printf("Can't convert filename to UTF16 %s\n", fullfilename)
		return err
	}
	attr, err := windows.GetFileAttributes(ptrFilenameUint16)
	if err != nil {
		log.Printf("Can't get file attributes: %s\n", err)
		return err
	}
	if attr&windows.FILE_ATTRIBUTE_ARCHIVE != 0 {
		err := windows.SetFileAttributes(ptrFilenameUint16, attr^windows.FILE_ATTRIBUTE_ARCHIVE)
		if err != nil {
			log.Printf("Can't set file archive attribute to 0: %s\n", err)
			return err
		}
	}
	return nil

}

func getArchiveAttribute(fullfilename string) (bool, error) {
	ptrFilename, err := windows.UTF16PtrFromString(fullfilename)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(ptrFilename)
	if err != nil {
		return false, err
	}
	return (attrs & windows.FILE_ATTRIBUTE_ARCHIVE) != 0, nil
}
