//go:generate stringer -type Kind

//Package errstr is used in every packages of he module to represent errors.
package errstr

import (
	"bytes"
	"fmt"
	"strconv"
)

// A New way of doing errors things.

const (
	// exported consts used throughout packages
	// I use unique errors codes amongst many related packages.
	// 0 - 999 reserved error codes.
	// Next constants define codes ranges for every package.
	ErrorsCodesPackageUploader                 = 1000
	ErrorsCodesPackageUploadserver             = 2000
	ErrorsCodesPackageLogins                   = 3000
	ErrorsCodesPackageFsdriver                 = 4000
	ErrorsCodesPackageCmdUploaderserver        = 5000
	ErrorsCodesPackageHttpDigestAuthentication = 6000
	ErrorsCodesPackageLiteImp                  = 7000
)
const (
	// Exported errors for all packages:
	// If there is no need to work with specific error - I use one of this groups of errors.
	// Error groups used by all packages and are exported.

	// ErrNotFound means 'not found' group of errors. Used from other packages.
	ErrNotFound int16 = iota
	// ErrFileIO for errors in reading or writing
	ErrFileIO
	// ErrPermission for errors in permission(rights)
	ErrPermission
	// ErrNetworkIO for errors in networking
	ErrNetworkIO
	// ErrEncodingDecoding for errors in various encoding(marchaling).
	ErrEncodingDecoding
	// ErrLimits for errors concerning limits
	ErrLimits
	// // ErrUploadIsNotAllowed
	// ErrUploadIsNotAllowed
	// // ErrSeccessfullUpload
	// ErrSeccessfullUpload
)

func ToUser(op string, code int16, descr string) error {
	return E(op, nil, code, 0, descr)
}

// New error
func New(op string, err error, code int16) error {
	return E(op, err, code, 0, "")
}

// E creates new Error based on base error. Below Error has .Unwrap().
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
	// ErrUseKindFromBaseError not sure about this
	ErrUseKindFromBaseError Kind = iota
	// ErrKindInfoForUsers not sure about this
	ErrKindInfoForUsers
)

// Error struct represents an error all over this module.
type Error struct {
	// Op is a function name in with error has occured. Makes finding error origins easy.
	Op    string // current function name that failed ex. fsdriver.func1(), for ease of failures origin identification
	Code  int16  // id of an error, used to create description of an error.
	Kind  Kind   // error group
	Descr string
	Err   error // underlying error
}

// Error satisfies error interface.
// The Error.Kind defines the format of string representation of Error.
// ErrUseKindFromBaseError = default is to print all hierarchy for current error.
// ErrKindInfoForUsers = is for user friendly messages.
func (e *Error) Error() string {
	var b bytes.Buffer

	switch e.Kind {
	case ErrKindInfoForUsers:
		// user friendly message
		b.WriteString("from ")
		b.WriteString(e.Op)
		b.WriteString(": ")
		if I18 != nil {
			b.WriteString(I18[e.Code])
		}
		if e.Descr != "" {
			b.WriteString(": ")
			b.WriteString(e.Descr)

			// TODO(zavla): internationalization of messages wait.
			// if I18m != nil {
			// 	// there is a translation
			// 	b.WriteString(I18m[e.Descr])
			// } else {
			// 	// internationalization is not loaded
			// 	b.WriteString(e.Descr)

			// }

		}

	default:
		// prints inner details
		b.WriteString("Error: ")
		b.WriteString(e.Op)

		b.WriteString(": ")
		b.WriteString(strconv.Itoa(int(e.Code)))

		b.WriteString(":")
		if I18 != nil {
			b.WriteString(I18[e.Code])
		}

		b.WriteString(":")
		b.WriteString(e.Descr)

		if e.Err != nil {
			const newline = ":\n\t"
			b.WriteString(newline)
			b.WriteString(e.Err.Error()) // RECURSIVE Error print
		}
	}
	return b.String()
}

// Unwrap now is a member of 'error' interface
func (e *Error) Unwrap() error {
	return e.Err
}

// I18 a map for error messages in selected language
var I18 = make(map[int16]string)

// I18m is a map for user messages
var I18m = make(map[string]string)

// I18text localizer
func I18text(id string, args ...interface{}) string {
	if I18m != nil {
		m, ok := I18m[id]
		if ok {
			return fmt.Sprintf(m, args...)

		}
	}
	return fmt.Sprintf(id, args...)
}

//------------------------------------------
// Next line of code are an old way of working with errors in this module.

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
