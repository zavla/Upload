package uploadclient_test

import (
	"context"
	"crypto/sha1"
	"io"
	"net/http/cookiejar"
	"os"
	"time"

	"github.com/zavla/upload/uploadclient"
	"golang.org/x/net/publicsuffix"
)

func ExampleSendAFile() {
	// a jar to hold our cookies
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})

	// specify where to connect
	config := uploadclient.ConnectConfig{
		ToURL:    "https://127.0.0.1:64000/upload/testuser",
		Password: "testuser",
		//PasswordHash: string, // you better to use a hash of a password
		Username:           "testuser",
		InsecureSkipVerify: true, // skips certificates chain test, don't use 'true' in production, of course use a CAPool!
		//CApool: *x509.CertPool, // a root CA public certificate that signed a service's certificate

	}

	// use context to have a cancel of a long running upload process
	ctx, callmetofreeresources := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second*10),
	)
	defer callmetofreeresources()

	const filename = "./testdata/testfile.txt"

	// compute sha1
	sha1file := sha1get(filename)

	err := uploadclient.SendAFile(ctx, &config, filename, jar, sha1file)
	if err != nil {
		println(err.Error())
		return
	}
	println("Normal OK:")
	// Output:
	// Normal OK:
}

func sha1get(filename string) []byte {
	f, err := os.Open(filename)
	if err != nil {
		println(err)
		return nil
	}
	defer f.Close()
	sha1 := sha1.New()
	io.Copy(sha1, f)
	return sha1.Sum(nil)
}
