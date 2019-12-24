package main

import Error "Upload/errstr"

const (
	// errors used in this package
	ErrCantOpenFileForReading = iota + Error.ErrorsCodesPackageUploader // codes have range
	ErrCantGetFileProperties
	ErrCantCreateHttpRequest
	ErrCantConnectToServer
	ErrFileSeekErrorOffset
	ErrNumberOfRetriesExceeded
	ErrMarkFileFailed
	ErrServerForbiddesUpload
	ErrServerRespondedWithBadJson
	ErrReadingDirectory
)

func init() {
	// for Uploader package
	Error.I18[ErrCantOpenFileForReading] = "Файл не открывается на чтение."
	Error.I18[ErrCantGetFileProperties] = "Система не возвращает свойства файла."
	Error.I18[ErrCantCreateHttpRequest] = "Ошибка создания объекта языка."
	Error.I18[ErrCantConnectToServer] = "Нет подключения к сервису Upload."
	Error.I18[ErrFileSeekErrorOffset] = "Указана неправильная позиция в файле."
	Error.I18[ErrNumberOfRetriesExceeded] = "Количество попыток превышено."
	Error.I18[ErrMarkFileFailed] = "Не получилось отметить локальный файл как загруженный."
	Error.I18[ErrServerForbiddesUpload] = "Сервис Upload не разрешает загружать этот файл."
	Error.I18[ErrServerRespondedWithBadJson] = "Сервер ответил не ожидаемой структурой json."
	Error.I18[ErrReadingDirectory] = "Ошибка при чтении файлов каталога."
}
