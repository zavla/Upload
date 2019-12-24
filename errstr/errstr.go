//go:generate stringer -type Kind
package errstr

import (
	"bytes"
	"fmt"
)

// A New way of doing errors things.
const (
	// exported consts used throughout packages
	// I use unique error codes amongst many related packages
	// 0 - 999 reserved error codes
	// codes have ranges for every package
	ErrorsCodesPackageUploader                 = 1000
	ErrorsCodesPackageUploadserver             = 2000
	ErrorsCodesPackageLogins                   = 3000
	ErrorsCodesPackageFsdriver                 = 4000
	ErrorsCodesPackageCmdUploaderserver        = 5000
	ErrorsCodesPackageHttpDigestAuthentication = 6000
)
const (
	// Exported errors for all packages:
	// If there is no need to work with specific error - I use just a group.
	// Error groups used by all packages and are exported.
	ErrNotFound int16 = iota
	ErrFileIO
	ErrPermission
	ErrNetworkIO
	ErrEncodingDecoding
	ErrLimits
)

// E creates new Error based on base error, I use Go1.13 errors.Unwrap, errors.Is, errors.As
func E(op string, err error, code int16, kind Kind, descr string) error {
	if kind == ErrUseKindFromBaseError {
		if errError, ok := err.(*Error); ok {
			// take Kind from base error
			kind = errError.Kind
		}
	}
	return &Error{
		Op:    op,
		Code:  code,
		Kind:  kind,
		Err:   err,
		Descr: descr,
	}
}

// Kind defines types of errors. Use "go generate" to update String() method.
type Kind uint8

const (
	ErrUseKindFromBaseError Kind = iota
	//------- informational messages
	ErrKindInfoForUsers
)

type Error struct {
	// Op is a function name in with error has occured. Makes finding error origins easy.
	Op    string // current function name that failed ex. fsdriver.func1(), for ease of failures origin identification
	Code  int16  // id of an error, used to create description of an error.
	Kind  Kind   // error group
	Descr string
	Err   error // underlying error
}

// error interface
func (e *Error) Error() string {
	const newline = ":\n\t"
	var b bytes.Buffer
	b.WriteString(e.Op)
	b.WriteString(": ")
	b.WriteString(e.Descr)
	if I18 != nil {
		b.WriteString(":")
		b.WriteString(I18[e.Code])
	}
	b.WriteString(": ")
	if e.Kind != ErrUseKindFromBaseError {
		b.WriteString(e.Kind.String())
	}

	b.WriteString(newline)
	if e.Err != nil {
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap now is a member of 'error' interface
func (e *Error) Unwrap() error {
	return e.Err
}

// I18 must be filled with messages in selected language
var I18 = make(map[int16]string)

//------------------------------------------
// Next code is an old way of doing errors things in this package.

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
