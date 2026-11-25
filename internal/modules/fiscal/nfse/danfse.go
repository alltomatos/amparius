package nfse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrDANFSeNotAuthorized impede gerar impressao de documento sem NFS-e
	// autorizada e persistida.
	ErrDANFSeNotAuthorized = errors.New("DANFSe exige NFS-e autorizada")
	// ErrInvalidDANFSeRequest rejeita entrada incompleta antes de qualquer
	// renderizacao.
	ErrInvalidDANFSeRequest = errors.New("requisicao DANFSe invalida")
)

// DANFSeInput e a chave de leitura usada pelo caso de uso autenticado.
type DANFSeInput struct {
	DocumentID uuid.UUID
	Competence time.Time
}

// DANFSeRequest e o contrato de impressao do DANFSe. Os bytes refletem o que
// ja foi autorizado e persistido; DPSXML e AuthorizationRaw sao opacos e
// somente leitura. Nenhum campo e transmitido a um servico externo.
type DANFSeRequest struct {
	TenantID, CompanyID, EstablishmentID uuid.UUID
	AccessKey, IDDps, Environment        string
	Competence, IssuedAt                 time.Time
	DPSXML, AuthorizationRaw             []byte
}

// DANFSeDocument e o resultado deterministico do renderer. Hash e o SHA-256
// dos bytes PDF, permitindo conferir idempotencia sem comparar o arquivo.
type DANFSeDocument struct {
	AccessKey, IDDps string
	Competence       time.Time
	IssuedAt         time.Time
	ContentType      string
	Bytes            []byte
	Hash             string
}

// DANFSeRenderer e o port de impressao. Implementacoes reais exigem a NFS-e
// oficial; nesta fatia apenas o renderer fake deterministico existe.
type DANFSeRenderer interface {
	Render(ctx context.Context, request DANFSeRequest) (DANFSeDocument, error)
}

// FakeDANFSeRenderer produz um PDF minimo, deterministico e explicitamente
// nao fiscal. Ele existe para fechar o contrato sem transmissao real; nao
// substitui o DANFSe oficial nem o XML da NFS-e.
type FakeDANFSeRenderer struct{}

func NewFakeDANFSeRenderer() *FakeDANFSeRenderer { return &FakeDANFSeRenderer{} }

func (r *FakeDANFSeRenderer) Render(ctx context.Context, request DANFSeRequest) (DANFSeDocument, error) {
	if err := ctx.Err(); err != nil {
		return DANFSeDocument{}, err
	}
	if strings.TrimSpace(request.AccessKey) == "" || strings.TrimSpace(request.IDDps) == "" || request.Competence.IsZero() {
		return DANFSeDocument{}, fmt.Errorf("%w: chave de acesso, idDps e competencia sao obrigatorios", ErrInvalidDANFSeRequest)
	}
	issuedAt := request.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = request.Competence
	}
	issuedAt = issuedAt.UTC()
	lines := []string{
		"DANFSe simulada - sem validade fiscal",
		"Chave de acesso: " + request.AccessKey,
		"idDPS: " + request.IDDps,
		"Competencia: " + request.Competence.UTC().Format("2006-01-02"),
		"Ambiente: " + request.Environment,
		"Emitida em: " + issuedAt.Format(time.RFC3339),
	}
	raw := buildSimplePDF("DANFSe", lines)
	return DANFSeDocument{
		AccessKey: request.AccessKey, IDDps: request.IDDps,
		Competence: request.Competence.UTC(), IssuedAt: issuedAt,
		ContentType: "application/pdf", Bytes: raw, Hash: SHA256Hex(raw),
	}, nil
}

// buildSimplePDF monta um PDF 1.4 de uma pagina com offsets de xref corretos.
// A saida depende apenas de title e lines, garantindo hash estavel.
func buildSimplePDF(title string, lines []string) []byte {
	var content strings.Builder
	content.WriteString("BT\n/F1 12 Tf\n40 800 Td\n")
	content.WriteString("(" + pdfEscape(title) + ") Tj\n0 -20 Td\n/F1 10 Tf\n")
	for _, line := range lines {
		content.WriteString("(" + pdfEscape(line) + ") Tj\n0 -14 Td\n")
	}
	content.WriteString("ET\n")
	stream := content.String()

	var out bytes.Buffer
	offsets := make([]int, 6)
	out.WriteString("%PDF-1.4\n")
	writeObject := func(number int, body string) {
		offsets[number] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", number, body)
	}
	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObject(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>")
	writeObject(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	offsets[5] = out.Len()
	fmt.Fprintf(&out, "5 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(stream), stream)
	xref := out.Len()
	out.WriteString("xref\n0 6\n")
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return out.Bytes()
}

// pdfEscape mantem o PDF em ASCII imprimivel e escapa os delimitadores de
// string literal.
func pdfEscape(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '(', ')':
			out.WriteByte('\\')
			out.WriteRune(r)
		default:
			if r < 32 || r > 126 {
				out.WriteByte('?')
				continue
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}
