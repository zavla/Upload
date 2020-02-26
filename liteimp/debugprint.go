// +build debugprint

package liteimp

import (
	"log"
)

func Debugprint(format string, args ...interface{}) {
	log.Printf(format, args...)
}
