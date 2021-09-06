package main

import (
	"os"
	"os/signal"

	"github.com/zavla/upload/uploadserver"
)

func runsAsService(config uploadserver.Config) {
	config.InitInterfacesConfigs()
	uploadserver.Debugprint("%#v", config)
	go endlessRunHTTPserver(&config)

	chSignals := make(chan os.Signal, 1)
	signal.Notify(chSignals)
	for {
		sig := <-chSignals
		switch sig {
		case os.Kill:
		case os.Interrupt:
			goto end
			// TODO(zavla): reload config?
		default:
		}
	}
end:
}
