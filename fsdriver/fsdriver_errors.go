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
	errActualFileNeedsRepare
	errActualFileAlreadyBiggerThanExpacted
	errActualFileIsAlreadyCompleteButJournalFileExists
	errActualFileIsAlreadyCompleteButJournalFileIsInconsistent
)

func init() {
	Error.I18[errPartialFileWritingError] = "Transaction Log file writing error."
	Error.I18[errPartialFileVersionTagReadError] = "Transaction Log file header read error."
	Error.I18[errPartialFileReadingError] = "Transaction Log file reading error."
	Error.I18[errPartialFileCorrupted] = "Transaction Log file corrupted."
	Error.I18[errForbidenToUpdateAFile] = "File already exists, and can't be overwritten."
	Error.I18[errPartialFileVersionTagUnsupported] = "Transaction Log file version unsupported."
	Error.I18[errPartialFileCreate] = "Transaction Log file creation error."
	Error.I18[errActualFileNeedsRepare] = "Actual file is needed of finding a maximum correct range."
	Error.I18[errActualFileAlreadyBiggerThanExpacted] = "Actual file is already bigger then expected."
	Error.I18[errActualFileIsAlreadyCompleteButJournalFileExists] = "Actual file is already complete but a journal file still exists."
	Error.I18[errActualFileIsAlreadyCompleteButJournalFileIsInconsistent] = "Actual file is completed but journal file still exists."
}
