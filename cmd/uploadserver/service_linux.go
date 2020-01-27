package main

import "upload/uploadserver"

func runsAsService(config uploadserver.Config) {
	runHTTPserver(config)
}
