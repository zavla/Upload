package logins

import (
	"Upload/httpDigestAuthentication"
	"testing"
)

func TestWriteLoginsJSON(t *testing.T) {
	type args struct {
		name   string
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

	loginsstruct := Logins{Version: "1", Logins: examplelogins}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "init a login file",
			args: args{name: "./logins.file", logins: loginsstruct},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := WriteLoginsJSON(tt.args.name, tt.args.logins); (err != nil) != tt.wantErr {
				t.Errorf("WriteLoginsJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
