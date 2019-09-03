package liteimp

import (
	"Upload/errstr"
	"fmt"
	"io/ioutil"
	"net/http"
)

const KeysessionID = "sessionId"

var (
	ErrSeccessfullUpload  = *errstr.NewError("ServerResponse", 1, "Upload successfully completed.")
	ErrUploadIsNorAllowed = *errstr.NewError("ServerResponse", 2, "Upload is not allowed.")
)

type RequestForUpload struct {
	Filename string `json:"filename" form:"filename"` // Url Query parameter
}
type QueryParamsToContinueUpload struct {
	Filename    string `json:"filename" form:"filename"`
	Startoffset int64  `json:"startoffset" form:"startoffset"`
	Count       int64  `json:"count" form:"count"`
}

type JsonResponse struct {
	Startoffset int64
	Count       int64 // expected bytes count
}

// JsonFileStatus is used by clients
type JsonFileStatus struct {
	JsonResponse
}

// Debugprint to print Response
func Debugprint(resp interface{}) {
	switch v := resp.(type) {
	case http.Response:
		// DEBUG ---------
		fmt.Printf("%v\n", v.Status)
		fmt.Printf("HEADERS\n")
		for k, v := range v.Header {
			fmt.Printf("%s: %v\n", k, v)
		}
		b, _ := ioutil.ReadAll(v.Body)
		fmt.Printf("\n%s\n", string(b))
		// END DEBUG ----------
	default:
		fmt.Printf("%v", v)

	}
	return
}
