package uploadserver

import Error "Upload/errstr"

const (
	errServerExpectsRestOfTheFile = iota + Error.ErrorsCodesPackageUploadserver
	errConnectionReadError
	errWrongURLParameters
	errRequestedFileIsBusy
	errUnexpectedFuncReturn
	errContentHeaderRequired
	errServerFailToWriteAllbytes
	errClientRequestShouldBindToJson
	errSessionEnded
	errWrongFuncParameters
	errSha1CheckFailed
)

func init() {
	Error.I18[errServerExpectsRestOfTheFile] = "Service expects the you send the rest of the file."
	Error.I18[errConnectionReadError] = "Connection read error."
	Error.I18[errWrongURLParameters] = "Wrong URL parameter(s)."
	Error.I18[errRequestedFileIsBusy] = "Requested filename is busy at the moment"
	Error.I18[errUnexpectedFuncReturn] = "Internal error. Unexpected return value from a function."
	Error.I18[errContentHeaderRequired] = "You must specify a Content-Length header for your new file."
	Error.I18[errServerFailToWriteAllbytes] = "Server failed to write all the bytes."
	Error.I18[errClientRequestShouldBindToJson] = "Your request must bind to a particular JSON structure."
	Error.I18[errSessionEnded] = "Your session has ended."
	Error.I18[errWrongFuncParameters] = "Wrong input parameter(s)."
	Error.I18[errSha1CheckFailed] = "Checksum of the file is wrong."
}
