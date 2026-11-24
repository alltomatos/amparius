package party_test

import (
	"testing"

	"github.com/alltomatos/amparius/internal/modules/party"
)

func TestPartyDisplayNameSemantics(t *testing.T) {
	social := "Nome de uso"
	empty := ""
	blank := "   "

	for _, test := range []struct {
		name  string
		party party.Party
		want  string
	}{
		{
			name:  "pessoa sem nome social",
			party: party.Party{Kind: party.KindPerson, Name: "Nome civil"},
			want:  "Nome civil",
		},
		{
			name:  "pessoa com nome social nulo",
			party: party.Party{Kind: party.KindPerson, Name: "Nome civil", SocialName: nil},
			want:  "Nome civil",
		},
		{
			name:  "pessoa com nome social vazio",
			party: party.Party{Kind: party.KindPerson, Name: "Nome civil", SocialName: &empty},
			want:  "Nome civil",
		},
		{
			name:  "pessoa com nome social em branco",
			party: party.Party{Kind: party.KindPerson, Name: "Nome civil", SocialName: &blank},
			want:  "Nome civil",
		},
		{
			name:  "pessoa com nome social preenchido",
			party: party.Party{Kind: party.KindPerson, Name: "Nome civil", SocialName: &social},
			want:  "Nome de uso",
		},
		{
			name:  "pessoa jurídica ignora nome social",
			party: party.Party{Kind: party.KindCompany, Name: "ACME LTDA", SocialName: &social},
			want:  "ACME LTDA",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.party.DisplayName(); got != test.want {
				t.Fatalf("DisplayName() = %q, esperado %q", got, test.want)
			}
			if got := test.party.CivilName(); got != test.party.Name {
				t.Fatalf("CivilName() = %q, esperado %q", got, test.party.Name)
			}
		})
	}
}
