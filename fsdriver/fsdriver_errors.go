package fsdriver

import Error "upload/errstr"

const (
	// errors used in this package
	errPartialFileWritingError = iota + Error.ErrorsCodesPackageFsdriver //codes have range
	errPartialFileVersionTagReadError
	errPartialFileReadingError
	errPartialFileCorrupted
	errForbidenToUpdateAFile
	errPartialFileVersionTagUnsupported
	errPartialFileCreate
)

func init() {
	Error.I18[errPartialFileWritingError] = "Transaction Log file writing error."
	Error.I18[errPartialFileVersionTagReadError] = "Transaction Log file header read error."
	Error.I18[errPartialFileReadingError] = "Transaction Log file reading error."
	Error.I18[errPartialFileCorrupted] = "Transaction Log file corrupted."
	Error.I18[errForbidenToUpdateAFile] = "File already exists, and can't be overwritten."
	Error.I18[errPartialFileVersionTagUnsupported] = "Transaction Log file version unsupported."
	Error.I18[errPartialFileCreate] = "Transaction Log file creation error."
}
