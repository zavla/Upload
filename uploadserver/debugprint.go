// +build debugprint

package uploadserver

import (
	"log"
)

func debugprint(format string, args...interface{}) {
	log.Printf(format, args...)
}

