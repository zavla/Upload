package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const bindAddress = `http://127.0.0.1:64000/upload?&Filename=sendfile.rar`

func SendAFile(addr string, dir, name string) error {

	fullfilename := filepath.Join(dir, name)
	f, err := os.OpenFile(fullfilename, os.O_RDONLY, 0)
	if err != nil {
		log.Fatalf("%s File: %s", err, fullfilename)
	}
	defer f.Close()

	req, err := http.NewRequest("GET", bindAddress, f)
	if err != nil {
		log.Fatalf("%s. Can't create http.Request.\n", err)
	}
	cli := &http.Client{Timeout: 50 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		log.Fatalf("%s. Can't connect...", err)
	}
	fmt.Printf("%v", resp.Status)
	fmt.Printf("HEADERS\n")
	for k, v := range resp.Header {
		fmt.Printf("%s  %v\n", k, v)
	}
	b, err := ioutil.ReadAll(resp.Body)
	fmt.Printf("\n%s\n", string(b))
	return err

	// b, err := ioutil.ReadAll(f)
	// n, err := conn.Write(b)
	// if err != nil {
	// 	log.Fatalf("%s", err)
	// }
	// fmt.Printf("%d bytes send\n", n)
	// time.Sleep(30 * time.Second)
}

func main() {
	SendAFile(bindAddress, "f:/Zavla_VB/GO/src/Upload/", "sendfile.rar")
}
