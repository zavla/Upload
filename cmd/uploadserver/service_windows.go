package main

import (
	"log"

	"github.com/zavla/upload/uploadserver"

	"golang.org/x/sys/windows/svc"
)

// works with Windows Service Control Manager

// Tservice represents my service and has a method Execute
type Tservice struct {
	config uploadserver.Config
}

// Execute is a method (callback) that responds to Service Control Manager (Windows API) requests.
func (s *Tservice) Execute(args []string, changerequest <-chan svc.ChangeRequest, updatestatus chan<- svc.Status) (specificreturn bool, errno uint32) {
	updatestatus <- svc.Status{State: svc.StartPending}
	// here we are only when windows starts this service
	s.config.InitInterfacesConfigs()
	uploadserver.Debugprint("%#v", s.config)
	go endlessRunHTTPserver(&s.config)

	supports := svc.AcceptStop | svc.AcceptShutdown

	updatestatus <- svc.Status{State: svc.Running, Accepts: supports}
	// select has no default and waits indefinitly
	select {
	case c := <-changerequest:
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			goto stoped
		case svc.Interrogate:

		}
	}
stoped:

	return false, 0
}

func runsAsService(config uploadserver.Config) {
	// a Windows variant
	err := svc.Run("upload", &Tservice{
		config: config,
	})
	if err != nil {
		log.Printf("windows svc.Run() exited with error %s\n", err)
	}

	//or a linux variant go runHTTPserver(config)
}
