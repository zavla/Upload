package fsdriver

import (
	"reflect"
	"testing"
)

func TestMayUpload(t *testing.T) {
	storegepath := "testdata"
	type ret struct {
		filestate FileState
		err       error
	}
	type args struct {
		storagepath string
		name        string
		ret         ret
	}
	type test struct {
		name string
		args args
	}
	tests := make([]test, 0, 10)
	tests = append(tests, test{
		name: "falseerrorInJournal",
		args: args{
			storagepath: storegepath,
			name:        "fop1_2019-09-01T13-30-00-660-differ.dif",
			ret: ret{
				filestate: FileState{
					fileProperties: fileProperties{FileSize: 69632},
					Startoffset:    0,
				},
				err: nil,
			},
		},
	})

	for _, tt := range tests {
		gotFilestate, goterr := MayUpload(tt.args.storagepath, tt.args.name)
		if !reflect.DeepEqual(gotFilestate, tt.args.ret.filestate) {
			t.Errorf("test %s, \nwant %#v, \ngot %#v", tt.name, tt.args.ret.filestate, gotFilestate)
		}
		if !reflect.DeepEqual(goterr, tt.args.ret.err) {
			t.Errorf("test %s, \nwant %v,\ngot %v", tt.name, tt.args.ret.err, goterr)
		}
	}
}
