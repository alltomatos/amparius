package outbox

import "time"

// Nomes de métricas da outbox. O prefixo amparius_ evita colisão e o sufixo
// segue a convenção Prometheus (_total para contador, _seconds para duração).
const (
	// MetricCyclesTotal conta ciclos de dispatch concluídos.
	MetricCyclesTotal = "amparius_outbox_cycles_total"
	// MetricCycleErrorsTotal conta ciclos que retornaram erro agregado.
	MetricCycleErrorsTotal = "amparius_outbox_cycle_errors_total"
	// MetricCycleDurationSeconds mede a duração de um ciclo completo.
	MetricCycleDurationSeconds = "amparius_outbox_cycle_duration_seconds"
	// MetricEventsClaimedTotal conta eventos reivindicados.
	MetricEventsClaimedTotal = "amparius_outbox_events_claimed_total"
	// MetricEventsSucceededTotal conta entregas concluídas.
	MetricEventsSucceededTotal = "amparius_outbox_events_succeeded_total"
	// MetricEventsRetriedTotal conta eventos reagendados para retry.
	MetricEventsRetriedTotal = "amparius_outbox_events_retried_total"
	// MetricEventsFailedTotal conta eventos em falha terminal.
	MetricEventsFailedTotal = "amparius_outbox_events_failed_total"
	// MetricEventsReapedTotal conta leases vencidos fechados por ReapExpired.
	MetricEventsReapedTotal = "amparius_outbox_events_reaped_total"
	// MetricBacklogEvents é o gauge por tenant e status
	// (pending, processing, failed).
	MetricBacklogEvents = "amparius_outbox_backlog_events"
	// MetricBacklogOldestAgeSeconds é a idade do evento mais antigo ainda
	// pendente ou em processamento, por tenant.
	MetricBacklogOldestAgeSeconds = "amparius_outbox_backlog_oldest_age_seconds"
	// MetricBacklogSampleErrorsTotal conta falhas ao amostrar o backlog; a
	// amostragem nunca interrompe a entrega.
	MetricBacklogSampleErrorsTotal = "amparius_outbox_backlog_sample_errors_total"
	// MetricDeliveryDurationSeconds mede o tempo do handler por evento.
	MetricDeliveryDurationSeconds = "amparius_outbox_delivery_duration_seconds"
)

// Backlog é a fotografia da fila de um tenant, sempre sob RLS.
type Backlog struct {
	Pending          int64
	Processing       int64
	Failed           int64
	OldestPendingAge time.Duration
}

// Total devolve pendentes + em processamento, o backlog acionável.
func (b Backlog) Total() int64 { return b.Pending + b.Processing }
