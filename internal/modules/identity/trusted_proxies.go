package identity

import (
	"fmt"
	"net"
	"strings"
)

// TrustedProxies restringe quais peers imediatos (RemoteAddr) têm permissão
// para que clientIPFrom confie no cabeçalho X-Forwarded-For (correção #2 do
// parecer do Analista sobre AUTH-01B): o cabeçalho é informado pelo próprio
// chamador HTTP e, sem uma allowlist, qualquer cliente pode forjar o IP usado
// no rate-limit de login só para escapar do limite por IP+e-mail. Sem CIDRs
// configurados (nil/vazio), o cabeçalho é sempre ignorado e o IP vem de
// RemoteAddr — esse é o padrão de produção até existir um proxy reverso
// confiável documentado.
type TrustedProxies struct {
	nets []*net.IPNet
}

// NewTrustedProxies analisa a lista de CIDRs confiáveis (ex.: o load balancer
// ou proxy reverso que termina TLS). Uma lista vazia devolve um valor que
// nunca confia em X-Forwarded-For.
func NewTrustedProxies(cidrs []string) (*TrustedProxies, error) {
	tp := &TrustedProxies{}
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("CIDR de proxy confiável inválido %q: %w", raw, err)
		}
		tp.nets = append(tp.nets, ipnet)
	}
	return tp, nil
}

// trusts informa se o IP do peer imediato está na allowlist. Um
// *TrustedProxies nil (zero value não construído) nunca confia em nada.
func (tp *TrustedProxies) trusts(ip net.IP) bool {
	if tp == nil || ip == nil {
		return false
	}
	for _, n := range tp.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
