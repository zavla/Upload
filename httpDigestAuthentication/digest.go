// httpDigestAuthentication implements server side of rfc2617: HTTP Authentication: Digest Access Authentication.
package httpDigestAuthentication

import (
	Error "Upload/errstr"
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"math/rand"
	"strings"
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
	Qop        string
	NonceCount string
	Cnonce     string
	Method     string
	Response   string
}

// GenerateWWWAuthenticate 	generates the "WWW-Authenticate" header that holds http digest authentication challenge.
// 'Digest realm=qweqwe, nonce=qweqwe, opaque=qweqwe, stale=qweqwe, algorithm=md5, domain=qweqwe, qop=qweqwe'
func GenerateWWWAuthenticate(c ChallengeToClient) string {

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
	//require c.Qop, this gives us a client side cnonce value.
	b.WriteString(", qop=")
	b.WriteString(c.Qop)

	return b.String()
}

// HashUsernameRealmPassword returns a string that one may save to a password database.
func HashUsernameRealmPassword(username, realm, password string) string {
	return md5hex(fmt.Sprintf("%s:%s:%s", username, realm, password))
}

// CheckCredentialsFromClient checks clients password with digest method.
func CheckCredentialsFromClient(c *ChallengeToClient, creds *CredentialsFromClient, hashUsernameRealmPassword string) (bool, error) {
	const op = "httpDigestAuthentication.CheckCredentialsFromClient()"
	if creds.Algorithm != "MD5" {
		return false, Error.E(op, nil, errBadDigestAlgorithm, 0, "")
	}

	if c.Nonce != creds.Nonce || c.Opaque != creds.Opaque || c.Realm != creds.Realm {
		// a user mangled the challenge on purpose
		return false, Error.E(op, nil, errClientHasMangledChallenge, 0, "")
	}
	responsewant, err := generateAuthorizationResponseParameter(hashUsernameRealmPassword, creds)
	if err != nil {
		return false, err
	}
	if responsewant != creds.Response {
		return false, Error.E(op, nil, errBadDigestResponse, 0, "")
	}

	return true, nil
}

// ParseStringIntoCredentialsFromClient extracts digest parameters from string
func ParseStringIntoCredentialsFromClient(input string) (*CredentialsFromClient, error) {
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

// generateAuthorizationResponseParameter creates 'response' parameter on server side. We compare it with the client 'response'.
// cr CredentialsFromClient is used to get all input parameters and _must_ be previously checked against ChallengeToClient.
func generateAuthorizationResponseParameter(hashUsernameRealmPassword string, cr *CredentialsFromClient) (string, error) {
	const op = "httpDigestAuthentication.GenerateAuthorizationString()"
	hashOfMethodAndUri := h2(cr.Method, cr.URI)
	s2 := ""
	switch cr.Qop {
	case "auth":
		// must use a cnonce from client
		s2 = fmt.Sprintf("%s:%s:%s:%s:%s", cr.Nonce, cr.NonceCount, cr.Cnonce, cr.Qop, hashOfMethodAndUri)

	case "":
		s2 = fmt.Sprintf("%s:%s", cr.Nonce, hashOfMethodAndUri)
	default:
		return string(rand.Int()), Error.E(op, nil, errUnsupportedMessageQop, 0, "")

	}

	return h2(hashUsernameRealmPassword, s2), nil

}
