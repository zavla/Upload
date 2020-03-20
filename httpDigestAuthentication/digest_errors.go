package httpDigestAuthentication

import Error "github.com/zavla/upload/errstr"

const (
	errBadAuthorization = iota + Error.ErrorsCodesPackageHttpDigestAuthentication
	errBadDigestAlgorithm
	errClientHasMangledChallenge
	errBadDigestResponse
	errUnsupportedMessageQop
)

func init() {
	Error.I18[errBadAuthorization] = "Authorization header from client is incorrect."
	Error.I18[errBadDigestAlgorithm] = "Unsupported digest algorithm."
	Error.I18[errClientHasMangledChallenge] = "Client returned mangled challenge parameters."
	Error.I18[errBadDigestResponse] = "Bad authorization string from client."
	Error.I18[errUnsupportedMessageQop] = "Bad Qop field from client."
}
