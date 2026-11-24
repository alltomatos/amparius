package nfse

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// OfficialDPSInput contem APENAS valores ja resolvidos pelo caso de uso a
// partir de tax_calc, fiscal_config e sessao autenticada. Nenhum codigo de
// servico, aliquota, NCM, CST, CFOP, regime ou valor monetario e definido
// aqui: o builder so serializa e falha fechado quando algo falta.
//
// Os campos usam os nomes do leiaute oficial v1.01 (TCDPS/TCInfDPS em
// docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposComplexos_v1.01.xsd).
type OfficialDPSInput struct {
	// Identificacao e controle
	ID       string `json:"id"`
	Versao   string `json:"versao"`
	TpAmb    string `json:"tp_amb"`
	DhEmi    string `json:"dh_emi"`
	VerAplic string `json:"ver_aplic"`
	Serie    string `json:"serie"`
	NDPS     string `json:"n_dps"`
	DCompet  string `json:"d_compet"`
	TpEmit   string `json:"tp_emit"`
	CLocEmi  string `json:"c_loc_emi"`

	Prestador OfficialPrestador `json:"prestador"`
	Servico   OfficialServico   `json:"servico"`
	Valores   OfficialValores   `json:"valores"`
}

// OfficialPrestador e o minimo de TCInfoPrestador + TCRegTrib.
// DocumentoTipo seleciona a alternativa do xs:choice (CNPJ/CPF/NIF/cNaoNIF).
type OfficialPrestador struct {
	DocumentoTipo string `json:"documento_tipo"`
	Documento     string `json:"documento"`
	OpSimpNac     string `json:"op_simp_nac"`
	RegEspTrib    string `json:"reg_esp_trib"`
}

// OfficialServico e o minimo de TCServ (TCLocPrest + TCCServ).
type OfficialServico struct {
	CLocPrestacao string `json:"c_loc_prestacao"`
	CTribNac      string `json:"c_trib_nac"`
	XDescServ     string `json:"x_desc_serv"`
}

// OfficialValores e o minimo de TCInfoValores: TCVServPrest (vServ),
// TCTribMunicipal (tribISSQN + tpRetISSQN) e TCTribTotal (indTotTrib).
// pAliq e demais campos de tributo sao opcionais e NAO sao preenchidos com
// constantes: quando necessarios, entram por campos de entrada futuros.
type OfficialValores struct {
	VServ      string `json:"v_serv"`
	TribISSQN  string `json:"trib_issqn"`
	TpRetISSQN string `json:"tp_ret_issqn"`
	IndTotTrib string `json:"ind_tot_trib"`
}

var officialDocumentKinds = map[string]bool{
	"CNPJ": true, "CPF": true, "NIF": true, "cNaoNIF": true,
}

// BuildDPSOficial monta a DPS no leiaute oficial v1.01, separada do builder
// estrutural BuildDPS (que permanece incompativel com o XSD e e usado pelos
// golden deterministicos da fatia 01). Nao transmite, nao calcula tributo e
// nao acessa rede.
func BuildDPSOficial(ctx context.Context, input OfficialDPSInput) (DPS, error) {
	if err := ctx.Err(); err != nil {
		return DPS{}, err
	}
	if err := validateOfficialInput(input); err != nil {
		return DPS{}, err
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<DPS xmlns="` + NamespaceDPS + `" versao="` + escape(input.Versao) + `">`)
	b.WriteString(`<infDPS Id="` + escape(input.ID) + `">`)
	b.WriteString(elem("tpAmb", input.TpAmb))
	b.WriteString(elem("dhEmi", input.DhEmi))
	b.WriteString(elem("verAplic", input.VerAplic))
	b.WriteString(elem("serie", input.Serie))
	b.WriteString(elem("nDPS", input.NDPS))
	b.WriteString(elem("dCompet", input.DCompet))
	b.WriteString(elem("tpEmit", input.TpEmit))
	b.WriteString(elem("cLocEmi", input.CLocEmi))
	b.WriteString(`<prest>`)
	b.WriteString(elem(input.Prestador.DocumentoTipo, input.Prestador.Documento))
	b.WriteString(`<regTrib>`)
	b.WriteString(elem("opSimpNac", input.Prestador.OpSimpNac))
	b.WriteString(elem("regEspTrib", input.Prestador.RegEspTrib))
	b.WriteString(`</regTrib>`)
	b.WriteString(`</prest>`)
	b.WriteString(`<serv>`)
	b.WriteString(`<locPrest>` + elem("cLocPrestacao", input.Servico.CLocPrestacao) + `</locPrest>`)
	b.WriteString(`<cServ>`)
	b.WriteString(elem("cTribNac", input.Servico.CTribNac))
	b.WriteString(elem("xDescServ", input.Servico.XDescServ))
	b.WriteString(`</cServ>`)
	b.WriteString(`</serv>`)
	b.WriteString(`<valores>`)
	b.WriteString(`<vServPrest>` + elem("vServ", input.Valores.VServ) + `</vServPrest>`)
	b.WriteString(`<trib>`)
	b.WriteString(`<tribMun>`)
	b.WriteString(elem("tribISSQN", input.Valores.TribISSQN))
	b.WriteString(elem("tpRetISSQN", input.Valores.TpRetISSQN))
	b.WriteString(`</tribMun>`)
	b.WriteString(`<totTrib>` + elem("indTotTrib", input.Valores.IndTotTrib) + `</totTrib>`)
	b.WriteString(`</trib>`)
	b.WriteString(`</valores>`)
	b.WriteString(`</infDPS>`)
	b.WriteString(`</DPS>`)

	raw := []byte(b.String())
	return DPS{XML: raw, XMLHash: SHA256Hex(raw), IDDps: input.ID}, nil
}

func validateOfficialInput(input OfficialDPSInput) error {
	obrigatorios := []struct{ nome, valor string }{
		{"Id", input.ID}, {"versao", input.Versao}, {"tpAmb", input.TpAmb},
		{"dhEmi", input.DhEmi}, {"verAplic", input.VerAplic}, {"serie", input.Serie},
		{"nDPS", input.NDPS}, {"dCompet", input.DCompet}, {"tpEmit", input.TpEmit},
		{"cLocEmi", input.CLocEmi},
		{"prest.documento_tipo", input.Prestador.DocumentoTipo},
		{"prest.documento", input.Prestador.Documento},
		{"prest.opSimpNac", input.Prestador.OpSimpNac},
		{"prest.regEspTrib", input.Prestador.RegEspTrib},
		{"serv.cLocPrestacao", input.Servico.CLocPrestacao},
		{"serv.cTribNac", input.Servico.CTribNac},
		{"serv.xDescServ", input.Servico.XDescServ},
		{"valores.vServ", input.Valores.VServ},
		{"valores.tribISSQN", input.Valores.TribISSQN},
		{"valores.tpRetISSQN", input.Valores.TpRetISSQN},
		{"valores.indTotTrib", input.Valores.IndTotTrib},
	}
	for _, campo := range obrigatorios {
		if strings.TrimSpace(campo.valor) == "" {
			return fmt.Errorf("%w: campo obrigatorio ausente: %s", ErrInvalidDPS, campo.nome)
		}
	}
	if !officialDocumentKinds[input.Prestador.DocumentoTipo] {
		return fmt.Errorf("%w: documento_tipo fora do choice oficial: %s", ErrInvalidDPS, input.Prestador.DocumentoTipo)
	}
	if _, err := time.Parse("2006-01-02", input.DCompet); err != nil {
		return fmt.Errorf("%w: dCompet invalido: %v", ErrInvalidDPS, err)
	}
	return nil
}

func elem(name, value string) string {
	return `<` + name + `>` + escape(value) + `</` + name + `>`
}
