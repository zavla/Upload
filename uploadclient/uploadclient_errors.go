package uploadclient

import Error "github.com/zavla/upload/errstr"

const (
	// errors used in this package
	errCantOpenFileForReading = iota + Error.ErrorsCodesPackageUploader // codes have range
	errCantGetFileProperties
	errCantCreateHTTPRequest
	errCantConnectToServer
	errFileSeekErrorOffset
	errNumberOfRetriesExceeded
	errMarkFileFailed
	errServerForbiddesUpload
	errServerRespondedWithBadJSON
	errReadingDirectory
	errBadHTTPAuthanticationMethod
	errBadHTTPAuthenticationChellenge
	// ErrAuthorizationFailed used at uploader
	ErrAuthorizationFailed
	errCanceled
	errWhileSendingARequestToServer
	errServerDidntProveItHasPasswordhash
	errFtpDialFailed
)

func init() {
	// for Uploader package
	//------------------ERRORS
	Error.I18[errCantOpenFileForReading] = "Файл не открывается на чтение."
	Error.I18[errCantGetFileProperties] = "Система не возвращает свойства файла."
	Error.I18[errCantCreateHTTPRequest] = "Ошибка создания объекта языка."
	Error.I18[errCantConnectToServer] = "Нет подключения к сервису Upload."
	Error.I18[errFileSeekErrorOffset] = "Указана неправильная позиция в файле."
	Error.I18[errNumberOfRetriesExceeded] = "Количество попыток превышено."
	Error.I18[errMarkFileFailed] = "Не получилось отметить локальный файл как загруженный."
	Error.I18[errServerForbiddesUpload] = "Сервис Upload не разрешает загружать этот файл."
	Error.I18[errServerRespondedWithBadJSON] = "Сервер ответил не ожидаемой структурой json."
	Error.I18[errReadingDirectory] = "Ошибка при чтении файлов каталога."
	Error.I18[errBadHTTPAuthanticationMethod] = "Не поддреживаемый метод http аутентификации. Только Digest."
	Error.I18[errBadHTTPAuthenticationChellenge] = "Заголовок WWW-Authentication неправильный."
	Error.I18[ErrAuthorizationFailed] = "Authorization failed."
	Error.I18[errCanceled] = "Sending canceled."
	Error.I18[errWhileSendingARequestToServer] = "Some error while sending a request to server."
	Error.I18[errServerDidntProveItHasPasswordhash] = "Server didn't prove it has the right passwordhash."
	Error.I18[errFtpDialFailed] = "Error with ftp, can't connect."
	//---------------------MESSAGES
	//Error.I18m["Server sider didn't prove it has a right password hash."] = ""
}
