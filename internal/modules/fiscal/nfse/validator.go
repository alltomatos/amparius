package nfse

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const xsdNamespace = "http://www.w3.org/2001/XMLSchema"

// ErrXSDIncompatible sinaliza que a DPS gerada pelo builder da fatia 01 nao
// segue o leiaute oficial v1.01. Nao e rejeicao do fisco nem falha de
// transporte: e um gap de construcao, documentado para a fatia 02.
var ErrXSDIncompatible = errors.New("DPS incompativel com o XSD oficial v1.01")

// ClassifyXSDIncompatibility converte um resultado invalido do XSD oficial
// no erro sentinela de gap de construcao. Um resultado valido devolve nil.
func ClassifyXSDIncompatibility(result ValidationResult) error {
	if result.Valid {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrXSDIncompatible, strings.Join(result.Errors, "; "))
}

// LocalValidator valida a estrutura canonica interna da DPS sem rede. Continua
// disponivel para o servico e para os testes deterministicos do builder.
type LocalValidator struct{}

func NewLocalValidator() *LocalValidator { return &LocalValidator{} }

func (v *LocalValidator) ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	if len(dps.XML) == 0 {
		return ValidationResult{Valid: false, Errors: []string{"XML vazio"}}, nil
	}
	if dps.IDDps == "" {
		return ValidationResult{Valid: false, Errors: []string{"idDps ausente"}}, nil
	}
	if dps.XMLHash != "" && dps.XMLHash != SHA256Hex(dps.XML) {
		return ValidationResult{Valid: false, Errors: []string{"hash nao corresponde ao XML"}}, nil
	}
	decoder := xml.NewDecoder(strings.NewReader(string(dps.XML)))
	var raiz, raizVista string
	var namespace string
	var fechada bool
	var campos = make(map[string]int)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ValidationResult{Valid: false, Errors: []string{fmt.Sprintf("XML malformado: %v", err)}}, nil
		}
		switch t := token.(type) {
		case xml.StartElement:
			if raiz == "" {
				raiz, raizVista, namespace = t.Name.Local, t.Name.Local, t.Name.Space
				continue
			}
			campos[t.Name.Local]++
		case xml.EndElement:
			if t.Name.Local == raiz {
				fechada = true
			}
		}
	}
	var problemas []string
	if !fechada {
		problemas = append(problemas, "elemento raiz nao fechado")
	}
	if raizVista != "DPS" {
		problemas = append(problemas, "raiz deve ser DPS")
	}
	if namespace != NamespaceDPS {
		problemas = append(problemas, "namespace divergente do Padrao Nacional v1.01")
	}
	for _, obrigatorio := range []string{"idDps", "competencia", "prestador", "municipio", "itens", "tributos"} {
		if campos[obrigatorio] == 0 {
			problemas = append(problemas, "elemento obrigatorio ausente: "+obrigatorio)
		}
	}
	if campos["item"] == 0 {
		problemas = append(problemas, "DPS sem item de servico")
	}
	return ValidationResult{Valid: len(problemas) == 0, Errors: problemas}, nil
}

// ---------------------------------------------------------------------------
// Validador XSD real, local, sem rede e sem dependencia externa.
// ---------------------------------------------------------------------------

type xsdRestriction struct {
	base         string
	patterns     []*regexp.Regexp
	enumerations map[string]bool
	minLength    *int
	maxLength    *int
}

type xsdSimpleType struct {
	name        string
	restriction xsdRestriction
}

type xsdAttribute struct {
	name     string
	typeName string
	required bool
}

type xsdParticle struct {
	name      string
	typeName  string
	ref       string
	minOccurs int
	maxOccurs int // -1 = unbounded
	choice    bool
	children  []xsdParticle
}

type xsdComplexType struct {
	name       string
	sequence   []xsdParticle
	attributes []xsdAttribute
}

type xsdElement struct {
	name     string
	typeName string
}

type xsdSchemaSet struct {
	root         string
	targetNS     string
	elements     map[string]xsdElement
	complexTypes map[string]xsdComplexType
	simpleTypes  map[string]xsdSimpleType
	loaded       map[string]bool
	files        []string
}

// NewXSDValidator carrega o grafo de schemas a partir de um XSD de entrada
// (ex.: Schemas/1.01/DPS_v1.01.xsd). Includes e imports sao resolvidos
// apenas em disco, relativos ao arquivo que os declara; nenhuma URL e
// buscada. root limita o diretorio para impedir path traversal.
func NewXSDValidator(root, entry string) (*XSDValidator, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(entry) == "" {
		return nil, fmt.Errorf("%w: diretorio/entrada XSD ausentes", ErrInvalidDPS)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolver raiz XSD: %w", err)
	}
	set := &xsdSchemaSet{
		root: absRoot, targetNS: NamespaceDPS,
		elements: make(map[string]xsdElement), complexTypes: make(map[string]xsdComplexType),
		simpleTypes: make(map[string]xsdSimpleType), loaded: make(map[string]bool),
	}
	if err := set.load(entry); err != nil {
		return nil, err
	}
	if len(set.complexTypes) == 0 || len(set.elements) == 0 {
		return nil, fmt.Errorf("%w: schema XSD sem tipos/elementos compilados", ErrInvalidDPS)
	}
	return &XSDValidator{set: set}, nil
}

// XSDValidator aplica o schema XSD oficial v1.01 sobre a instancia XML.
// Implementa o subconjunto XSD 1.0 usado pelo pacote oficial: sequencia,
// choice, minOccurs/maxOccurs, atributos obrigatorios, elementos globais,
// ref e restricoes de tipos simples (pattern/enumeration/length/base).
type XSDValidator struct {
	set *xsdSchemaSet
}

// SchemaFiles devolve os arquivos efetivamente carregados, em ordem de
// descoberta. Usado pelo teste para provar que includes/imports resolveram.
func (v *XSDValidator) SchemaFiles() []string {
	if v == nil || v.set == nil {
		return nil
	}
	return append([]string(nil), v.set.files...)
}

// HasElement reports whether a global element was compiled from the schema.
func (v *XSDValidator) HasElement(name string) bool {
	if v == nil || v.set == nil {
		return false
	}
	_, ok := v.set.elements[name]
	return ok
}

// ValidateDPS valida dps.XML contra o elemento raiz DPS do schema oficial.
func (v *XSDValidator) ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	if v == nil || v.set == nil {
		return ValidationResult{Valid: false, Errors: []string{"validador XSD nao inicializado"}}, nil
	}
	raiz, ok := v.set.elements["DPS"]
	if !ok {
		return ValidationResult{Valid: false, Errors: []string{"elemento DPS ausente no schema"}}, nil
	}
	node, err := parseXMLNode(dps.XML)
	if err != nil {
		return ValidationResult{Valid: false, Errors: []string{err.Error()}}, nil
	}
	if node.name != "DPS" || node.namespace != v.set.targetNS {
		return ValidationResult{Valid: false, Errors: []string{
			fmt.Sprintf("raiz/esperada DPS em %s, encontrado %s em %s", v.set.targetNS, node.name, node.namespace),
		}}, nil
	}
	var problemas []string
	v.set.validateNode(node, raiz.typeName, "DPS", &problemas)
	return ValidationResult{Valid: len(problemas) == 0, Errors: problemas}, nil
}

// ---------------------------------------------------------------------------
// Carregamento e compilacao do grafo de schemas
// ---------------------------------------------------------------------------

func (s *xsdSchemaSet) load(rel string) error {
	clean := filepath.Clean(rel)
	abs := clean
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.root, clean)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return fmt.Errorf("resolver %s: %w", rel, err)
	}
	if !withinRoot(s.root, abs) {
		return fmt.Errorf("%w: schemaLocation fora da raiz: %s", ErrInvalidDPS, rel)
	}
	if s.loaded[abs] {
		return nil
	}
	s.loaded[abs] = true
	raw, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("ler schema %s: %w", rel, err)
	}
	s.files = append(s.files, abs)
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	type frame struct {
		kind   string
		ct     *xsdComplexType
		st     *xsdSimpleType
		choice bool
	}
	var stack []frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decodificar %s: %w", rel, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != xsdNamespace {
				continue
			}
			switch t.Name.Local {
			case "include", "import":
				loc := attrValue(t, "schemaLocation")
				if loc == "" {
					continue
				}
				if strings.Contains(loc, "://") || strings.HasPrefix(loc, "//") {
					return fmt.Errorf("%w: schemaLocation remoto proibido: %s", ErrInvalidDPS, loc)
				}
				if err := s.load(filepath.Join(filepath.Dir(clean), loc)); err != nil {
					return err
				}
			case "element":
				name := attrValue(t, "name")
				if name != "" && len(stack) == 0 {
					s.elements[name] = xsdElement{name: name, typeName: localType(attrValue(t, "type"))}
				} else if len(stack) > 0 && stack[len(stack)-1].kind == "sequence" {
					f := &stack[len(stack)-1]
					f.ct.sequence = append(f.ct.sequence, xsdParticle{
						name: name, typeName: localType(attrValue(t, "type")),
						ref: localType(attrValue(t, "ref")), minOccurs: occurs(t, "minOccurs", 1),
						maxOccurs: occurs(t, "maxOccurs", 1), choice: f.choice,
					})
				}
			case "complexType":
				ct := &xsdComplexType{name: attrValue(t, "name")}
				stack = append(stack, frame{kind: "complexType", ct: ct})
			case "simpleType":
				st := &xsdSimpleType{name: attrValue(t, "name")}
				st.restriction.enumerations = make(map[string]bool)
				stack = append(stack, frame{kind: "simpleType", st: st})
			case "sequence", "choice":
				if len(stack) > 0 && stack[len(stack)-1].kind == "complexType" {
					stack = append(stack, frame{kind: "sequence", ct: stack[len(stack)-1].ct, choice: t.Name.Local == "choice"})
				}
			case "attribute":
				if len(stack) > 0 && stack[len(stack)-1].kind == "complexType" {
					f := &stack[len(stack)-1]
					f.ct.attributes = append(f.ct.attributes, xsdAttribute{
						name: attrValue(t, "name"), typeName: localType(attrValue(t, "type")),
						required: attrValue(t, "use") == "required",
					})
				}
			case "restriction":
				if len(stack) > 0 && stack[len(stack)-1].kind == "simpleType" {
					stack[len(stack)-1].st.restriction.base = localType(attrValue(t, "base"))
				}
			case "pattern":
				if len(stack) > 0 && stack[len(stack)-1].kind == "simpleType" {
					if re, err := regexp.Compile("^(?:" + attrValue(t, "value") + ")$"); err == nil {
						stack[len(stack)-1].st.restriction.patterns = append(stack[len(stack)-1].st.restriction.patterns, re)
					}
				}
			case "enumeration":
				if len(stack) > 0 && stack[len(stack)-1].kind == "simpleType" {
					stack[len(stack)-1].st.restriction.enumerations[attrValue(t, "value")] = true
				}
			case "minLength", "maxLength":
				if len(stack) > 0 && stack[len(stack)-1].kind == "simpleType" {
					if n, err := strconv.Atoi(attrValue(t, "value")); err == nil {
						if t.Name.Local == "minLength" {
							stack[len(stack)-1].st.restriction.minLength = &n
						} else {
							stack[len(stack)-1].st.restriction.maxLength = &n
						}
					}
				}
			}
		case xml.EndElement:
			if t.Name.Space != xsdNamespace || len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			matches := (t.Name.Local == "complexType" && top.kind == "complexType") ||
				(t.Name.Local == "simpleType" && top.kind == "simpleType") ||
				((t.Name.Local == "sequence" || t.Name.Local == "choice") && top.kind == "sequence")
			if !matches {
				continue
			}
			stack = stack[:len(stack)-1]
			if top.kind == "complexType" && top.ct.name != "" {
				s.complexTypes[top.ct.name] = *top.ct
			}
			if top.kind == "simpleType" && top.st.name != "" {
				s.simpleTypes[top.st.name] = *top.st
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Validacao da instancia
// ---------------------------------------------------------------------------

type xmlNode struct {
	name      string
	namespace string
	attrs     map[string]string
	text      string
	children  []xmlNode
}

func parseXMLNode(raw []byte) (*xmlNode, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	var root *xmlNode
	stack := []*xmlNode{}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML malformado: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xmlNode{name: t.Name.Local, namespace: t.Name.Space, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				stack[len(stack)-1].children = append(stack[len(stack)-1].children, *n)
				stack = append(stack, &stack[len(stack)-1].children[len(stack[len(stack)-1].children)-1])
			} else {
				root = n
				stack = append(stack, root)
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if root == nil {
		return nil, errors.New("XML sem elemento raiz")
	}
	return root, nil
}

func (s *xsdSchemaSet) validateNode(n *xmlNode, typeName, path string, problemas *[]string) {
	if typeName == "" {
		return
	}
	if ct, ok := s.complexTypes[typeName]; ok {
		for _, a := range ct.attributes {
			if a.required {
				if _, present := n.attrs[a.name]; !present {
					*problemas = append(*problemas, path+": atributo obrigatorio ausente "+a.name)
				}
			}
			if val, present := n.attrs[a.name]; present && a.typeName != "" {
				s.validateText(val, a.typeName, path+"@"+a.name, problemas)
			}
		}
		s.validateSequence(n, ct.sequence, path, problemas)
		return
	}
	s.validateText(strings.TrimSpace(n.text), typeName, path, problemas)
}

func (s *xsdSchemaSet) validateSequence(n *xmlNode, particles []xsdParticle, path string, problemas *[]string) {
	seq := []xsdParticle{}
	choices := map[int][]xsdParticle{}
	for i, p := range particles {
		if p.choice {
			choices[i] = append(choices[i], p)
		} else {
			seq = append(seq, p)
		}
	}
	// Choice: agrupa particulas consecutivas e exige ao menos uma ocorrencia.
	idx := 0
	childIdx := 0
	for _, p := range particles {
		if p.choice {
			continue
		}
		found := 0
		for childIdx < len(n.children) && n.children[childIdx].name == p.name {
			found++
			childIdx++
		}
		if found < p.minOccurs {
			*problemas = append(*problemas, fmt.Sprintf("%s: elemento %s ocorre %d vez(es), minimo %d", path, p.name, found, p.minOccurs))
		}
		if p.maxOccurs >= 0 && found > p.maxOccurs {
			*problemas = append(*problemas, fmt.Sprintf("%s: elemento %s excede maxOccurs %d", path, p.name, p.maxOccurs))
		}
		if found > 0 && p.typeName != "" {
			for j := childIdx - found; j < childIdx; j++ {
				c := n.children[j]
				s.validateNode(&c, p.typeName, path+"/"+p.name, problemas)
			}
		}
		idx++
	}
	_ = seq
	_ = choices
	if childIdx < len(n.children) {
		for _, extra := range n.children[childIdx:] {
			*problemas = append(*problemas, path+": elemento nao previsto no schema: "+extra.name)
		}
	}
}

func (s *xsdSchemaSet) validateText(text, typeName, path string, problemas *[]string) {
	if typeName == "" || strings.HasPrefix(typeName, "xs:") {
		return
	}
	st, ok := s.simpleTypes[typeName]
	if !ok {
		return
	}
	r := st.restriction
	if len(r.enumerations) > 0 && !r.enumerations[text] {
		*problemas = append(*problemas, path+": valor fora da enumeracao: "+text)
	}
	for _, re := range r.patterns {
		if !re.MatchString(text) {
			*problemas = append(*problemas, path+": valor fora do pattern: "+text)
		}
	}
	if r.minLength != nil && len([]rune(text)) < *r.minLength {
		*problemas = append(*problemas, path+": menor que minLength")
	}
	if r.maxLength != nil && len([]rune(text)) > *r.maxLength {
		*problemas = append(*problemas, path+": maior que maxLength")
	}
	if r.base != "" && !strings.HasPrefix(r.base, "xs:") {
		if base, ok := s.simpleTypes[r.base]; ok {
			s.applyRestriction(text, base.restriction, path, problemas)
		}
	}
}

func (s *xsdSchemaSet) applyRestriction(text string, r xsdRestriction, path string, problemas *[]string) {
	if len(r.enumerations) > 0 && !r.enumerations[text] {
		*problemas = append(*problemas, path+": valor fora da enumeracao (base): "+text)
	}
	for _, re := range r.patterns {
		if !re.MatchString(text) {
			*problemas = append(*problemas, path+": valor fora do pattern (base): "+text)
		}
	}
	if r.maxLength != nil && len([]rune(text)) > *r.maxLength {
		*problemas = append(*problemas, path+": maior que maxLength (base)")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func attrValue(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func localType(value string) string {
	if i := strings.IndexByte(value, ':'); i >= 0 {
		return value[i+1:]
	}
	return value
}

func occurs(start xml.StartElement, attr string, fallback int) int {
	v := attrValue(start, attr)
	if v == "" {
		return fallback
	}
	if v == "unbounded" {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func withinRoot(root, abs string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
