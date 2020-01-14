.ONESHELL:
all : uploadserver uploader
.PHONY: all
.PHONY: uploader
.PHONY: uploadserver

uploader : 
	cd .\cmd\uploader
	go build -v

uploadserver :
	cd .\cmd\uploadserver 
	go build -v
