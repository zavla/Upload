// Package httpDigestAuthentication implements server side of rfc2617: HTTP Authentication: Digest Access Authentication.
package httpDigestAuthentication

// to debug run: ./uploader.exe --file ./testbackups/sendfile.rar
import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"math/rand"
	"strings"

	Error "github.com/zavla/upload/errstr"
)

// ChallengeToClient is used to hold parameters that will be sent to client in WWW-Authenticate header.
type ChallengeToClient struct {
	Realm     string
	Domain    string
	Nonce     string
	Opaque    string
	Stale     string
	Algorithm string
	Qop       string
}

// CredentialsFromClient is used to hold digest parameters that a client returned us.
type CredentialsFromClient struct {
	ChallengeToClient
	Username   string
	URI        string
	NonceCount string
	Cnonce     string
	Method     string
	Response   string
}

// GenerateAuthorization creates a http digest Authorization header at client side.
func GenerateAuthorization(c *CredentialsFromClient) string {
	//Digest username="zahar",realm="upload",nonce="72e86b2fb36b49f5521600618fee56bb",uri="/upload/zahar",cnonce="6bedbcd39a03430202a75c2e6602725b",nc=00000001,algorithm=MD5,response="cc03ba528bd9b48981689a7941fb9c91",qop="auth"
	b := &bytes.Buffer{}
	b.WriteString("Digest ")

	b.WriteString("username=")
	b.WriteString(c.Username)

	b.WriteString(", realm=")
	b.WriteString(c.Realm)

	if c.Nonce != "" {
		b.WriteString(", nonce=")
		b.WriteString(c.Nonce)
	}
	if c.URI != "" {
		b.WriteString(", uri=")
		b.WriteString(c.URI)
	}

	b.WriteString(", cnonce=")
	b.WriteString(c.Cnonce)

	if c.NonceCount != "" {
		b.WriteString(", nc=")
		b.WriteString(c.NonceCount)
	}
	if c.Opaque != "" {
		b.WriteString(", opaque=")
		b.WriteString(c.Opaque)
	}
	if c.Stale != "" {
		b.WriteString(", stale=")
		b.WriteString(c.Stale)
	}
	if c.Algorithm != "" {
		b.WriteString(", algorithm=")
		b.WriteString(c.Algorithm)
	}
	b.WriteString(", response=")
	b.WriteString(c.Response)

	b.WriteString(", qop=")
	b.WriteString(c.Qop)

	return b.String()
}

// HashUsernameRealmPassword returns a string that one may save to a password database.
func HashUsernameRealmPassword(username, realm, password string) string {
	return md5hex(fmt.Sprintf("%s:%s:%s", username, realm, password))
}

// GenerateWWWAuthenticate 	generates the "WWW-Authenticate" header that holds http digest authentication challenge.
// Used on server side.
// Returns for example: 'Digest realm=qweqwe, nonce=qweqwe, opaque=qweqwe, stale=qweqwe, algorithm=md5, domain=qweqwe, qop=qweqwe'
func GenerateWWWAuthenticate(c *ChallengeToClient) string {

	b := &bytes.Buffer{}
	b.WriteString("Digest ")
	b.WriteString("realm=")
	b.WriteString(c.Realm)
	if c.Domain != "" {
		b.WriteString(", domain=")
		b.WriteString(c.Domain)
	}
	if c.Nonce != "" {
		b.WriteString(", nonce=")
		b.WriteString(c.Nonce)
	}
	if c.Opaque != "" {
		b.WriteString(", opaque=")
		b.WriteString(c.Opaque)
	}
	if c.Stale != "" {
		b.WriteString(", stale=")
		b.WriteString(c.Stale)
	}
	if c.Algorithm != "" {
		b.WriteString(", algorithm=")
		b.WriteString(c.Algorithm)
	}
	// We require c.Qop, this gives us that a client side returns a cnonce value.
	b.WriteString(", qop=")
	b.WriteString(c.Qop)

	return b.String()
}

// CheckCredentialsFromClient checks clients password with digest method.
// It is used at server side.
func CheckCredentialsFromClient(c *ChallengeToClient, creds *CredentialsFromClient, hashUsernameRealmPassword string) (bool, error) {
	const op = "httpDigestAuthentication.CheckCredentialsFromClient()"
	if creds.Algorithm != "MD5" {
		return false, Error.E(op, nil, errBadDigestAlgorithm, 0, "")
	}

	if c.Nonce != creds.Nonce || c.Opaque != creds.Opaque || c.Realm != creds.Realm {
		// a user mangled the challenge on purpose
		return false, Error.E(op, nil, errClientHasMangledChallenge, 0, "")
	}
	responsewant, err := GenerateResponseAuthorizationParameter(hashUsernameRealmPassword, creds)
	if err != nil {
		return false, err
	}
	if responsewant != creds.Response {
		return false, nil // no access, just no error
	}

	return true, nil
}

// ParseStringIntoStruct extracts digest parameters from string.
// It is used both at server and client side to create a struct with extracted parameters.
func ParseStringIntoStruct(input string) (*CredentialsFromClient, error) {
	const op = "httpDigestAuthentication.parseStringToCredentialsFromClient()"
	ret := &CredentialsFromClient{}
	const quote = `"'`
	if !strings.HasPrefix(input, "Digest ") {
		return ret, Error.E(op, nil, errBadAuthorization, 0, "")
	}
	// deletes newlines
	s := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, input[7:])

	sl := strings.Split(s, ",")
	for _, kv := range sl {
		posEq := strings.IndexRune(kv, '=')
		if posEq >= 0 {
			k := strings.TrimSpace(kv[:posEq])
			v := strings.Trim(kv[posEq+1:], quote)
			switch k {
			case "realm":
				ret.Realm = v
			case "domain":
				ret.Domain = v
			case "nonce":
				ret.Nonce = v
			case "cnonce":
				ret.Cnonce = v
			case "response":
				ret.Response = v
			case "uri":
				ret.URI = v

			case "opaque":
				ret.Opaque = v
			case "stale":
				ret.Stale = v
			case "algorithm":
				ret.Algorithm = v
			case "qop":
				ret.Qop = v
			case "nc":
				ret.NonceCount = v
			case "username":
				ret.Username = v
			case "method":
				ret.Method = v

			default:
				return ret, Error.E(op, nil, errBadAuthorization, 0, "")
			}
		}
	}
	return ret, nil
}

func md5hex(s string) string {
	h := md5.New()
	_, _ = io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func h2(s1, s2 string) string {
	return md5hex(fmt.Sprintf("%s:%s", s1, s2))
}

// GenerateResponseAuthorizationParameter creates 'response' parameter.
// Server side uses it to validate clients response.
// Client side uses it to create 'response' parameter.
// On server side the cr *CredentialsFromClient is used to get all input parameters and _must_ be previously checked against ChallengeToClient.
func GenerateResponseAuthorizationParameter(hashUsernameRealmPassword string, cr *CredentialsFromClient) (string, error) {
	const op = "httpDigestAuthentication.GenerateAuthorizationString()"
	// don't forget to fill cr.Method
	hashOfMethodAndURI := h2(cr.Method, cr.URI)
	s2 := ""
	switch cr.Qop {
	case "auth":
		// must use a cnonce from client
		s2 = fmt.Sprintf("%s:%s:%s:%s:%s", cr.Nonce, cr.NonceCount, cr.Cnonce, cr.Qop, hashOfMethodAndURI)

	case "":
		s2 = fmt.Sprintf("%s:%s", cr.Nonce, hashOfMethodAndURI)
	default:
		return string(rand.Int()), Error.E(op, nil, errUnsupportedMessageQop, 0, "")

	}

	return h2(hashUsernameRealmPassword, s2), nil

}

// KeyProvePeerHasRightPasswordhash is used by clients to check the server side has the write password hash.
var KeyProvePeerHasRightPasswordhash = "X-ProveThatPeerHasTheRightHash"

// ProveThatPeerHasRightPasswordhash is used by clients to ask the server to prove that it has the write passwordhash.
// That is the server didn't just answered 'OK' on our authorization.
func ProveThatPeerHasRightPasswordhash(hashUsernameRealmPassword, ResponseFromClient string) string {
	return h2(hashUsernameRealmPassword, ResponseFromClient)
}
