package main

import Error "upload/errstr"

const (
	errCantWriteLogFile = iota + Error.ErrorsCodesPackageCmdUploaderserver
	errServiceExitedAbnormally
)

func init() {
	Error.I18[errCantWriteLogFile] = "Service can't start. Can't write to a log file."
	Error.I18[errServiceExitedAbnormally] = "Service exited abnormally."
}
