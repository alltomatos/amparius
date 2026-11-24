package identity

import "testing"

func TestDevelopmentRequestDerivaDocumentosPorChave(t *testing.T) {
	first := DevelopmentRequest("fixture-key-a")
	firstReplay := DevelopmentRequest("fixture-key-a")
	second := DevelopmentRequest("fixture-key-b")

	if first != firstReplay {
		t.Fatal("a mesma chave deveria gerar a mesma fixture")
	}
	if first.TenantDocument == second.TenantDocument {
		t.Fatal("chaves diferentes deveriam gerar tenant_document diferente")
	}
	if first.CompanyDocument == second.CompanyDocument || first.EstablishmentDocument == second.EstablishmentDocument {
		t.Fatal("chaves diferentes deveriam gerar documentos sintéticos diferentes")
	}
	if first.City != "Fortaleza" || first.State != "CE" || first.Zip != "60000-000" {
		t.Fatalf("local sintético = %q/%q/%q", first.City, first.State, first.Zip)
	}
}
