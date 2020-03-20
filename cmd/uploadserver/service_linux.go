package main

import "github.com/zavla/upload/uploadserver"

func runsAsService(config uploadserver.Config) {
	runHTTPserver(config)
}
