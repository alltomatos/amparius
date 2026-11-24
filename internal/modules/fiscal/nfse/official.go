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

	Prestador     OfficialPrestador `json:"prestador"`
	Tomador       *OfficialPessoa   `json:"tomador,omitempty"`
	Intermediario *OfficialPessoa   `json:"intermediario,omitempty"`
	Servico       OfficialServico   `json:"servico"`
	Valores       OfficialValores   `json:"valores"`
}

// OfficialPrestador e o minimo de TCInfoPrestador + TCRegTrib.
// DocumentoTipo seleciona a alternativa do xs:choice (CNPJ/CPF/NIF/cNaoNIF).
type OfficialPrestador struct {
	DocumentoTipo string `json:"documento_tipo"`
	Documento     string `json:"documento"`
	OpSimpNac     string `json:"op_simp_nac"`
	RegEspTrib    string `json:"reg_esp_trib"`
}

// OfficialPessoa representa os ramos opcionais toma/interm. O nome e
// obrigatorio pelo TCInfoPessoa; o documento seleciona o xs:choice oficial.
// Os valores continuam sendo entradas resolvidas pelo caso de uso e podem
// ser sinteticos apenas em fixtures de teste.
type OfficialPessoa struct {
	DocumentoTipo string `json:"documento_tipo"`
	Documento     string `json:"documento"`
	Nome          string `json:"nome"`
}

// OfficialServico e o minimo de TCServ (TCLocPrest + TCCServ).
type OfficialServico struct {
	CLocPrestacao string                    `json:"c_loc_prestacao"`
	CTribNac      string                    `json:"c_trib_nac"`
	XDescServ     string                    `json:"x_desc_serv"`
	InfoCompl     *OfficialInfoComplementar `json:"info_compl,omitempty"`
}

// OfficialInfoComplementar cobre apenas os campos livres de TCInfoCompl.
// Nao introduz codigo fiscal, aliquota ou regra legal.
type OfficialInfoComplementar struct {
	IDDocTec string `json:"id_doc_tec,omitempty"`
	DocRef   string `json:"doc_ref,omitempty"`
	XPed     string `json:"x_ped,omitempty"`
	XInfComp string `json:"x_inf_comp,omitempty"`
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

// Limites publicados nos simple types do xs:choice de documento nos grupos
// prest (TCInfoPrestador) e toma/interm (TCInfoPessoa). Nao ha regra fiscal
// aqui: apenas os facets do pacote XSD oficial v1.01.
const (
	officialCNPJLen = 14
	officialCPFLen  = 11
	officialNIFMax  = 40
	officialNomeMax = 300
)

// officialNaoNIFCodes e a enumeracao de TSCodNaoNIF (motivo da nao informacao
// do NIF): 0 = nao informado na nota de origem, 1 = dispensado, 2 = nao
// exigencia. Nenhum outro codigo e aceito.
var officialNaoNIFCodes = map[string]bool{"0": true, "1": true, "2": true}

// BuildDPSOficial monta a DPS no leiaute oficial v1.01, separada do builder
// estrutural BuildDPS (que permanece incompativel com o XSD e e usado pelos
// golden deterministicos da fatia 01). Nao transmite, nao calcula tributo e
// nao acessa rede.
func BuildDPSOficial(ctx context.Context, input OfficialDPSInput) (DPS, error) {
	if err := ctx.Err(); err != nil {
		return DPS{}, err
	}
	// Normaliza uma unica vez, antes de validar e serializar. Assim o
	// DocumentoTipo que passa no xs:choice e o mesmo que vira nome de elemento:
	// um tipo com espacos nao pode validar trimado e gerar "< CNPJ >" invalido.
	input = normalizeOfficialInput(input)
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
	if input.Tomador != nil {
		writeOfficialPessoa(&b, "toma", *input.Tomador)
	}
	if input.Intermediario != nil {
		writeOfficialPessoa(&b, "interm", *input.Intermediario)
	}
	b.WriteString(`<serv>`)
	b.WriteString(`<locPrest>` + elem("cLocPrestacao", input.Servico.CLocPrestacao) + `</locPrest>`)
	b.WriteString(`<cServ>`)
	b.WriteString(elem("cTribNac", input.Servico.CTribNac))
	b.WriteString(elem("xDescServ", input.Servico.XDescServ))
	b.WriteString(`</cServ>`)
	if input.Servico.InfoCompl != nil {
		b.WriteString(`<infoCompl>`)
		b.WriteString(elemIf("idDocTec", input.Servico.InfoCompl.IDDocTec))
		b.WriteString(elemIf("docRef", input.Servico.InfoCompl.DocRef))
		b.WriteString(elemIf("xPed", input.Servico.InfoCompl.XPed))
		b.WriteString(elemIf("xInfComp", input.Servico.InfoCompl.XInfComp))
		b.WriteString(`</infoCompl>`)
	}
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

// normalizeOfficialInput devolve uma copia do input com o DocumentoTipo de
// cada grupo sem espacos nas bordas. Copia as pessoas opcionais para nao
// alterar a struct do chamador. Normalizar uma vez e o que garante que a
// validacao e a serializacao usem exatamente o mesmo tipo.
func normalizeOfficialInput(input OfficialDPSInput) OfficialDPSInput {
	input.Prestador.DocumentoTipo = strings.TrimSpace(input.Prestador.DocumentoTipo)
	if input.Tomador != nil {
		tomador := *input.Tomador
		tomador.DocumentoTipo = strings.TrimSpace(tomador.DocumentoTipo)
		input.Tomador = &tomador
	}
	if input.Intermediario != nil {
		intermediario := *input.Intermediario
		intermediario.DocumentoTipo = strings.TrimSpace(intermediario.DocumentoTipo)
		input.Intermediario = &intermediario
	}
	return input
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
	if err := validateOfficialDocumento(input.Prestador.DocumentoTipo, input.Prestador.Documento, "prest"); err != nil {
		return err
	}
	if _, err := time.Parse("2006-01-02", input.DCompet); err != nil {
		return fmt.Errorf("%w: dCompet invalido: %v", ErrInvalidDPS, err)
	}
	if err := validateOfficialPessoa(input.Tomador, "toma"); err != nil {
		return err
	}
	if err := validateOfficialPessoa(input.Intermediario, "interm"); err != nil {
		return err
	}
	if input.Servico.InfoCompl != nil {
		if strings.TrimSpace(input.Servico.InfoCompl.IDDocTec) == "" &&
			strings.TrimSpace(input.Servico.InfoCompl.DocRef) == "" &&
			strings.TrimSpace(input.Servico.InfoCompl.XPed) == "" &&
			strings.TrimSpace(input.Servico.InfoCompl.XInfComp) == "" {
			return fmt.Errorf("%w: infoCompl sem nenhum campo", ErrInvalidDPS)
		}
	}
	return nil
}

func validateOfficialPessoa(pessoa *OfficialPessoa, grupo string) error {
	if pessoa == nil {
		return nil
	}
	if strings.TrimSpace(pessoa.Nome) == "" {
		return fmt.Errorf("%w: campo obrigatorio ausente: %s.nome", ErrInvalidDPS, grupo)
	}
	if tamanho := len([]rune(pessoa.Nome)); tamanho < 1 || tamanho > officialNomeMax {
		return fmt.Errorf("%w: %s.nome deve ter de 1 a %d caracteres", ErrInvalidDPS, grupo, officialNomeMax)
	}
	if strings.TrimSpace(pessoa.Documento) == "" {
		return fmt.Errorf("%w: campo obrigatorio ausente: %s.documento", ErrInvalidDPS, grupo)
	}
	return validateOfficialDocumento(pessoa.DocumentoTipo, pessoa.Documento, grupo)
}

// validateOfficialDocumento valida o xs:choice de documento pelo tipo
// escolhido, usando somente os facets oficiais:
//   - CNPJ: exatamente 14 digitos (TSCNPJ [0-9]{14});
//   - CPF: exatamente 11 digitos (TSCPF [0-9]{11});
//   - NIF: 1..40 caracteres (TSNIF minLength=1, maxLength=40);
//   - cNaoNIF: um codigo da enumeracao 0|1|2 (TSCodNaoNIF).
//
// O tipo fora do choice falha fechado, como antes. Um valor que nao casa com o
// facet do proprio tipo tambem falha fechado, em vez de chegar ao XSD.
//
// O chamador normaliza o input antes; aqui o tipo e comparado como veio, sem
// trim, para que um caminho nao normalizado falhe em vez de passar despercebido.
func validateOfficialDocumento(tipo, valor, grupo string) error {
	if !officialDocumentKinds[tipo] {
		return fmt.Errorf("%w: %s.documento_tipo fora do choice oficial: %s", ErrInvalidDPS, grupo, tipo)
	}
	campo := grupo + ".documento"
	switch tipo {
	case "CNPJ":
		if !isOfficialDigits(valor, officialCNPJLen) {
			return fmt.Errorf("%w: %s (%s) deve ter exatamente %d digitos", ErrInvalidDPS, campo, tipo, officialCNPJLen)
		}
	case "CPF":
		if !isOfficialDigits(valor, officialCPFLen) {
			return fmt.Errorf("%w: %s (%s) deve ter exatamente %d digitos", ErrInvalidDPS, campo, tipo, officialCPFLen)
		}
	case "NIF":
		// TSNIF: minLength=1, maxLength=40. O minimo ja e barrado antes pelo
		// campo obrigatorio; aqui ele e reafirmado para o validador ser fechado
		// por si so.
		if tamanho := len([]rune(valor)); tamanho < 1 || tamanho > officialNIFMax {
			return fmt.Errorf("%w: %s (%s) deve ter de 1 a %d caracteres", ErrInvalidDPS, campo, tipo, officialNIFMax)
		}
	case "cNaoNIF":
		if !officialNaoNIFCodes[valor] {
			return fmt.Errorf("%w: %s (cNaoNIF) fora da enumeracao 0|1|2: %s", ErrInvalidDPS, campo, valor)
		}
	}
	return nil
}

func isOfficialDigits(valor string, tamanho int) bool {
	if len(valor) != tamanho {
		return false
	}
	for _, r := range valor {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func writeOfficialPessoa(b *strings.Builder, grupo string, pessoa OfficialPessoa) {
	b.WriteString("<" + grupo + ">")
	b.WriteString(elem(pessoa.DocumentoTipo, pessoa.Documento))
	b.WriteString(elem("xNome", pessoa.Nome))
	b.WriteString("</" + grupo + ">")
}

func elem(name, value string) string {
	return `<` + name + `>` + escape(value) + `</` + name + `>`
}

func elemIf(name, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return elem(name, value)
}
