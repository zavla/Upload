// Support functions
package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/zavla/upload/logins"
)

func loadPemFileIntoCertPool(certpool *x509.CertPool, filename string) error {
	if certpool == nil {
		return errors.New("certpool is nil")
	}

	_, errCertPub := os.Stat(filename)

	if os.IsNotExist(errCertPub) {
		return errCertPub
	}

	pemCerts, err := ioutil.ReadFile(filename)
	if err == nil {
		ok := certpool.AppendCertsFromPEM(pemCerts)
		if !ok {
			return errors.New("failed to add a certificate to the pool of certificates")
		}
	}
	return nil

}

// AbsInput transforms each item of input with Abs.
// outputname are arguments the programmer decides at compile time.
// Used to absolutize each filename into your variable.
// Empty strings will be ignored.
func AbsInput(input []*string, outputnames ...*string) error {
	if len(input) != len(outputnames) {
		return fmt.Errorf("calling function AbsInput with len(input) != len(outputnames)")
	}
	for i := range input {
		if input[i] == nil {
			continue
		}
		if *input[i] == "" {
			continue // do not abs empty string
		}
		fullpath, err := filepath.Abs(*input[i])
		if err != nil {
			return fmt.Errorf("name %v is incorrect after Abs()", *input[i])
		}
		*outputnames[i] = fullpath
	}
	return nil
}

func gosavepassword(loginsSt logins.Logins, username string, forhttps bool) {
	savepasswordWithDPAPI(&loginsSt, username, forhttps, constRealm)
}
