package main

import (
	"flag"
	"log"
	"os"
	"upload/fsdriver"
)

var name string

func main() {
	flag.StringVar(&name, "file", "", "journal file")
	flag.Usage = func() { println(`Decodes specified a journal file`) }
	flag.Parse()
	if name == "" {
		flag.PrintDefaults()
		return
	}
	f, err := os.Open(name)
	if err != nil {
		log.Printf("%s\n", err)
		return

	}
	err = fsdriver.DecodePartialFile(f, os.Stdout)
	if err != nil {
		log.Printf("%s\n", err)
		return
	}
}
