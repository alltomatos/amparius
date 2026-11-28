package auth

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, algo, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if algo != PasswordAlgoArgon2idV1 {
		t.Fatalf("algo = %q, quer %q", algo, PasswordAlgoArgon2idV1)
	}
	ok, needsRehash, err := VerifyPassword(hash, algo, "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("senha correta deveria bater com o hash recém-gerado")
	}
	if needsRehash {
		t.Fatal("hash recém-gerado com parâmetros atuais não deveria exigir rehash")
	}
}

func TestVerifyPasswordWrongPassword(t *testing.T) {
	hash, algo, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, _, err := VerifyPassword(hash, algo, "wrong-password")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("senha errada não deveria bater com o hash")
	}
}

func TestVerifyPasswordOutdatedAlgoSignalsRehash(t *testing.T) {
	hash, _, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, needsRehash, err := VerifyPassword(hash, "argon2id:v0-legado", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("senha correta deveria bater independente do rótulo de versão")
	}
	if !needsRehash {
		t.Fatal("algo divergente do atual deveria sinalizar needsRehash")
	}
}

func TestVerifyPasswordInvalidHashFormat(t *testing.T) {
	if _, _, err := VerifyPassword("not-an-argon2-hash", PasswordAlgoArgon2idV1, "qualquer"); err == nil {
		t.Fatal("hash malformado deveria devolver erro")
	}
}

func TestDecoyHashNeverMatchesAndIsStable(t *testing.T) {
	first := DecoyHash()
	second := DecoyHash()
	if first != second {
		t.Fatal("DecoyHash deveria ser estável entre chamadas (memoizado)")
	}
	ok, _, err := VerifyPassword(first, PasswordAlgoArgon2idV1, "qualquer-senha-nunca-bate")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("hash de isca nunca deveria bater com uma senha real")
	}
}

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	if _, _, err := HashPassword(""); err == nil {
		t.Fatal("senha vazia deveria ser rejeitada")
	}
}
