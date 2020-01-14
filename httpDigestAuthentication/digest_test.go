package httpDigestAuthentication

import (
	"reflect"
	"testing"
)

func TestParseStringIntoCredentialsFromClient(t *testing.T) {
	type args struct {
		input string
	}
	tests := []struct {
		name    string
		args    args
		want    *CredentialsFromClient
		wantErr bool
	}{
		{name: "standard",
			args: args{input: `Digest username="Mufasa",
                 realm="testrealm@host.com",
                 nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093",
                 uri="/dir/index.html",
                 qop=auth,
                 nc=00000001,
                 cnonce="0a4f113b",
                 response="6629fae49393a05397450978507c4ef1",
                 opaque="5ccc069c403ebaf9f0171e9517f40e41"`},
			want: &CredentialsFromClient{
				ChallengeToClient: ChallengeToClient{
					Realm:     "testrealm@host.com",
					Domain:    "",
					Nonce:     "dcd98b7102dd2f0e8b11d0f600bfb0c093",
					Opaque:    "5ccc069c403ebaf9f0171e9517f40e41",
					Stale:     "",
					Algorithm: "",
					Qop:       "auth",
				},
				Username:   "Mufasa",
				URI:        "/dir/index.html",
				NonceCount: "00000001",
				Cnonce:     "0a4f113b",
				Method:     "",
				Response:   "6629fae49393a05397450978507c4ef1",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStringIntoStruct(tt.args.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStringIntoStruct() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseStringIntoStruct() \n\tgot = %v,\n\twant %v", got, tt.want)
			}
		})
	}
}

func TestGenerateAuthorizationResponseParameter(t *testing.T) {
	type args struct {
		hashUsernameRealmPassword string
		cr                        CredentialsFromClient
	}
	standardAuthorizationHeader := `Digest username="Mufasa",
                 realm="testrealm@host.com",
                 nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093",
                 uri="/dir/index.html",
                 qop=auth,
                 nc=00000001,
                 cnonce="0a4f113b",
                 response="6629fae49393a05397450978507c4ef1",
                 opaque="5ccc069c403ebaf9f0171e9517f40e41",
				 method="GET"`
	credentials, _ := ParseStringIntoStruct(standardAuthorizationHeader)

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{name: "standard response",
			args: args{
				hashUsernameRealmPassword: HashUsernameRealmPassword("Mufasa", "testrealm@host.com", "Circle Of Life"),
				cr:                        *credentials,
			},
			want:    "6629fae49393a05397450978507c4ef1",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateResponseAuthorizationParameter(tt.args.hashUsernameRealmPassword, &tt.args.cr)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateResponseAuthorizationParameter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GenerateResponseAuthorizationParameter() got = %v, want %v", got, tt.want)
			}
		})
	}
}
