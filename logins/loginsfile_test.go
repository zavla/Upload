package logins

import (
	"fmt"
	"testing"
	"upload/httpDigestAuthentication"
)

func TestWriteLoginsJSON(t *testing.T) {
	type args struct {
		logins Logins
	}
	examplelogins := make([]Login, 0)
	examplelogins = append(examplelogins, Login{
		Login:        "zahar",
		Email:        "z@b",
		Passwordhash: "",
		Disabled:     false,
		mu:           nil,
	})
	l := &examplelogins[0]
	l.Passwordhash = httpDigestAuthentication.HashUsernameRealmPassword(l.Login, "upload", "pass")

	examplelogins = append(examplelogins, Login{
		Login:        "test@beer-co.com",
		Email:        "test@b",
		Passwordhash: "",
		Disabled:     false,
		mu:           nil,
	})
	l = &examplelogins[1]
	l.Passwordhash = httpDigestAuthentication.HashUsernameRealmPassword(l.Login, "upload", "pass")

	loginsstruct := Logins{Version: "1",
		Logins:   examplelogins,
		filename: "./logins.json",
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "init a login file",
			args: args{logins: loginsstruct},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.args.logins.writeLoginsJSON(); (err != nil) != tt.wantErr {
				t.Errorf("WriteLoginsJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReadAddWriteRead(t *testing.T) {
	var err error
	Store, err = ReadLoginsJSON("./logins.json")
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	usr1, err := Store.Add("za1", "email@string", "pass1")
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	usr2, err := Store.Add("a2", "a@2", "pass1")
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	usr3, err := Store.Add("a1", "a@1", "pass1")
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	usr22, err := Store.Add("a2", "a@22", "pass2")
	if err != nil {
		t.Errorf("%s", err)
		return
	}

	err = Store.Save()
	if err != nil {
		t.Errorf("%s", err)
	}
	_ = usr2
	_ = usr22
	_ = usr1
	_ = usr3
	store1, err := ReadLoginsJSON("./logins.json")
	for _, v := range store1.Logins {
		fmt.Printf("%v\n", v)
	}

}

func TestGrowCapacityInvalidatePointers(t *testing.T) {
	type strV struct {
		n1 int
		n2 int
	}

	obj := strV{1, 2}
	a := make([]strV, 0, 0)
	a = append(a, obj)

	b := make([]*strV, 0, 0)
	b = append(b, &a[0])
	count := 655360
	for i := 1; i < count; i++ {
		a = append(a, strV{i, i})
		b = append(b, &a[i])
	}
	for i := 0; i < count; i++ {
		if &a[i] != b[i] {
			fmt.Printf("%v != %v  %p != %p \n", a[i], *b[i], &a[i], b[i])
		}
	}

}

func TestAskAndSavePasswordForHTTPDigest(t *testing.T) {
	type args struct {
		loginsmanager Manager
		loginobj      Login
		realm         string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := AskAndSavePasswordForHTTPDigest(tt.args.loginsmanager, tt.args.loginobj, tt.args.realm); (err != nil) != tt.wantErr {
				t.Errorf("AskAndSavePasswordForHTTPDigest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
