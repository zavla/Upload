package liteimp

import (
	Error "github.com/zavla/upload/errstr"
)

func init() {
	Error.I18[ErrUploadIsNotAllowed] = "Service doesn't allow to update the file."
	Error.I18[ErrSuccessfullUpload] = "Upload successfull."
}
