package fsdriver

import (
	"reflect"
	"testing"
)

func TestMayUpload(t *testing.T) {
	storegepath := "d:/tempstorage"
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
			name:        "ubcd_sklad_2010_2019-09-06T18-05-00-440-differ.dif", // ".partialinfo" will be added in MayUpload
			ret: ret{
				filestate: FileState{
					fileProperties: fileProperties{FileSize: 247431168},
					Startoffset:    146896703,
				},
				err: nil,
			},
		},
	})

	for _, tt := range tests {
		gotFilestate, goterr := MayUpload(tt.args.storagepath, tt.args.name)
		if !reflect.DeepEqual(gotFilestate, tt.args.ret.filestate) {
			t.Errorf("test %s, return filestate, want %#v, got %#v", tt.name, tt.args.ret.filestate, gotFilestate)
		}
		if !reflect.DeepEqual(goterr, tt.args.ret.err) {
			t.Errorf("test %s, return error, want %#v, got %#v", tt.name, tt.args.ret.err, goterr)
		}
	}
}
