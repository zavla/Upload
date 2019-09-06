package errstr

import "fmt"

// NewError creates a var in your code
func NewError(group string, code int, s string) *errstr {
	return &errstr{
		Code:  code,
		Group: group,
		S:     s,
	}
}

type errstr struct {
	Code    int
	Group   string // package name to make error codes unique
	S       string
	Details string
	Err     error
}

func (e errstr) SubError(parenterr error) *errstr {
	e.Err = parenterr
	return &e
}
func (e errstr) Error() string {
	return fmt.Sprintf(`Error in %s, #%d - %s, details %s, suberror: %s`, e.Group, e.Code, e.S, e.Details, e.Err)
}
func (e errstr) SetDetails(format string, args ...interface{}) errstr {
	e.Details = fmt.Sprintf(format, args...)
	return e // makes a self copy?
}
func (e errstr) SetDetailsSubErr(suberror error, format string, args ...interface{}) *errstr {
	e.Details = fmt.Sprintf(format, args...)
	e.Err = suberror
	return &e
}
