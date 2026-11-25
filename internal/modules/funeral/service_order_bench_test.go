package funeral

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func BenchmarkServiceOrderResponse(b *testing.B) {
	order := ServiceOrder{ID: uuid.New(), CompanyID: uuid.New(), ContractID: uuid.New(), Kind: "funeral_service", Description: "atendimento", Status: ServiceOrderCompleted, Version: 3, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	for i := 0; i < 10; i++ {
		order.StatusHistory = append(order.StatusHistory, ServiceOrderStatusChange{ID: uuid.New(), FromStatus: ServiceOrderOpen, ToStatus: ServiceOrderInProgress, ChangedAt: time.Now().UTC()})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = serviceOrderResponse(order)
	}
}
