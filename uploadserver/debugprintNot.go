// +build !debugprint

package uploadserver

// Debugprint is NOP unless go build -tags 'debugprint'
func Debugprint(format string, args ...interface{}) {

}
