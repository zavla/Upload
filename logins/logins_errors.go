package logins

import (
	"strconv"

	Error "github.com/zavla/upload/errstr"
)

const (
	errLoginNotFound = iota + Error.ErrorsCodesPackageLogins
	errTempFileCreationFailed
	errEncodingDecoding
	errSaveLogin
	errPackIntoBlockFailed
	errFileOpen
	// TODO(zavla): decide what to do with code REPRESENTATION
	errReadPassword
	errLoginsManagerCantAdd
	errLoginsManagerCantSave
)

func init() {
	Error.I18[errLoginNotFound] = strconv.Itoa(errLoginNotFound) + "Login not found."
	Error.I18[errTempFileCreationFailed] = "Temporary file creation failed."
	Error.I18[errEncodingDecoding] = "Error while encoding or decoding JSON."
	Error.I18[errSaveLogin] = "Login save error."
	Error.I18[errPackIntoBlockFailed] = "Pack login into block failed."
	Error.I18[errFileOpen] = "File open error."
	Error.I18[errReadPassword] = "Error while reading password."
	Error.I18[errLoginsManagerCantAdd] = "Can't add a login."
	Error.I18[errLoginsManagerCantSave] = "Can't save a login."
}
