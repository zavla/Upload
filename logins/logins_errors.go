package logins

import (
	Error "upload/errstr"
)

const (
	errLoginNotFound = iota + Error.ErrorsCodesPackageLogins
	errTempFileCreationFailed
	errEncodingDecoding
	errSaveLogin
	errPackIntoBlockFailed
	errFileOpen
)

func init() {
	Error.I18[errLoginNotFound] = "Login not found."
	Error.I18[errTempFileCreationFailed] = "Temporary file creation failed."
	Error.I18[errEncodingDecoding] = "Error while encoding or decoding JSON."
	Error.I18[errSaveLogin] = "Login save error."
	Error.I18[errPackIntoBlockFailed] = "Pack login into block failed."
	Error.I18[errFileOpen] = "File open error."
}
