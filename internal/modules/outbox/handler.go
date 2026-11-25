package outbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrNoDeliveryHandler indica que o worker foi ligado sem handler capaz de
// produzir efeito. O ADR 0009 proíbe marcar eventos como processados sem
// efeito: este handler existe para tornar essa recusa explícita e auditável,
// em vez de um noop silencioso que esvazia a outbox.
var ErrNoDeliveryHandler = errors.New("outbox: worker sem handler de entrega")

// RefusingHandler é o Handler de segurança usado quando o binário é ligado
// sem um efeito real registrado. Ele recusa toda entrega, propagando erro para
// o Dispatcher (que aplica retry/falha terminal) e impedindo Succeed. Não é um
// noop: um noop marcaria eventos como processados à toa, violando o contrato
// v1 e o ADR 0009.
type RefusingHandler struct{}

// Handle recusa a entrega sem tocar em dados de negócio.
func (RefusingHandler) Handle(context.Context, Event) error {
	return ErrNoDeliveryHandler
}

// DeliveryHandler é o handler real de entrega da OUT-01B. Ele não inventa
// efeito de negócio: valida o envelope v1 do evento (inclusive a guarda de
// PII) e devolve sucesso para que o Dispatcher grave a confirmação de entrega
// em Succeed, na mesma transação do status 'processed'. Payload nunca é
// logado, copiado para fora nem incluído em mensagens de erro.
type DeliveryHandler struct{}

// NewDeliveryHandler devolve o handler de entrega padrão do worker.
func NewDeliveryHandler() DeliveryHandler { return DeliveryHandler{} }

// Handle valida o envelope antes de autorizar a conclusão do evento.
func (DeliveryHandler) Handle(_ context.Context, event Event) error {
	if event.ID == uuid.Nil || event.TenantID == uuid.Nil || event.AggregateID == uuid.Nil {
		return fmt.Errorf("%w: envelope sem referências opacas obrigatórias", ErrInvalidEvent)
	}
	if !eventTypePattern.MatchString(event.EventType) {
		return fmt.Errorf("%w: event_type fora do contrato v1", ErrInvalidEvent)
	}
	if event.Version <= 0 {
		return fmt.Errorf("%w: event_version deve ser positivo", ErrInvalidEvent)
	}
	if event.Status != "processing" {
		return fmt.Errorf("%w: entrega exige evento em processing", ErrInvalidEvent)
	}
	if event.Attempts <= 0 {
		return fmt.Errorf("%w: entrega exige tentativa registrada", ErrInvalidEvent)
	}
	if err := ValidatePayloadPrivacy(event.Payload); err != nil {
		return err
	}
	return nil
}
