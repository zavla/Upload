// +build debugprint

package uploadserver

import (
	"log"
)

func Debugprint(format string, args ...interface{}) {
	log.Printf(format, args...)
}
