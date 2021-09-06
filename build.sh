#!/usr/bin/env bash 

git rev-list --pretty="format:%h %ai %ae" -1 HEAD > ./gitCommit
gg=$(cat ./gitCommit)

cd ./cmd/uploader
go build -v -ldflags="-X 'main.gitCommit=$gg'" 
cd ../..

cd ./cmd/uploadserver 
go build -v -ldflags="-X 'main.gitCommit=$gg'" 
cd ../..
