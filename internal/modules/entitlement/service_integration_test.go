package entitlement_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func newControlDatabase(t *testing.T) *db.DB {
	t.Helper()
	dsn := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_CONTROL_POSTGRES_DSN nao definido; integracao do control plane nao executada")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)

	var role string
	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper OR rolbypassrls
			  FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatalf("integracao exige papel sem SUPERUSER/BYPASSRLS; role=%s bypass=%v", role, bypass)
	}
	return database
}

func newControlService(t *testing.T) (*db.DB, *entitlement.Service) {
	t.Helper()
	database := newControlDatabase(t)
	service, err := entitlement.NewService(database)
	if err != nil {
		t.Fatal(err)
	}
	return database, service
}

// cleanupTenant remove licencas, cotas e assentos do tenant de teste. Os eventos
// de auditoria nao sao removidos: sao append-only por contrato, inclusive no
// banco, e ficam isolados pelo tenant aleatorio de cada execucao.
func cleanupTenant(t *testing.T, database *db.DB, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := db.WithTenant(context.Background(), tenantID)
		err := database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM entitlement.license_assignment WHERE tenant_id = $1`, tenantID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM entitlement.license_pool WHERE tenant_id = $1`, tenantID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `DELETE FROM entitlement.user_quota WHERE tenant_id = $1`, tenantID)
			return err
		})
		if err != nil {
			t.Errorf("cleanup do tenant %s: %v", tenantID, err)
		}
	})
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestEntitlementActivationAuditQuotaAndReactivation(t *testing.T) {
	database, service := newControlService(t)
	tenantID := uuid.New()
	ctx := db.WithTenant(context.Background(), tenantID)
	cleanupTenant(t, database, tenantID)

	companyA, companyB := uuid.New(), uuid.New()
	actor := uuid.New()
	module := entitlement.ModuleFloriculture

	if err := service.SetModuleQuota(ctx, module, 1); err != nil {
		t.Fatalf("provisionar cota: %v", err)
	}

	from := date(2026, time.November, 1)
	to := date(2026, time.December, 31)
	first, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: module,
		CompanyID:  companyA,
		Origin:     entitlement.OriginSelfService,
		ValidFrom:  &from,
		ValidTo:    &to,
	})
	if err != nil {
		t.Fatalf("ativar empresa A: %v", err)
	}
	if first.Status != entitlement.StatusActive {
		t.Fatalf("status esperado active, recebido %q", first.Status)
	}
	if !first.ValidFrom.Equal(from) || first.ValidTo == nil || !first.ValidTo.Equal(to) {
		t.Fatalf("vigencia inesperada: %v..%v", first.ValidFrom, first.ValidTo)
	}

	events, err := service.ListEvents(ctx, entitlement.EventFilter{ModuleCode: module, CompanyID: &companyA})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("ativacao deveria gerar 1 evento, recebidos %d", len(events))
	}
	event := events[0]
	if event.Action != entitlement.ActionActivated || event.ActorID != actor ||
		event.Origin != entitlement.OriginSelfService || event.At.IsZero() ||
		event.CompanyID != companyA || event.ModuleCode != module {
		t.Fatalf("auditoria incompleta: %+v", event)
	}

	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: module, CompanyID: companyB, Origin: entitlement.OriginSelfService,
		ValidFrom: &from, ValidTo: &to,
	}); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatalf("segunda empresa deveria exceder a cota de 1, recebido %v", err)
	}

	deactivated, err := service.Deactivate(ctx, actor, entitlement.TransitionInput{
		ModuleCode: module, CompanyID: companyA, Origin: entitlement.OriginSelfService,
	})
	if err != nil {
		t.Fatalf("desativar A: %v", err)
	}
	if deactivated.Status != entitlement.StatusInactive {
		t.Fatalf("status esperado inactive, recebido %q", deactivated.Status)
	}
	if deactivated.ID != first.ID {
		t.Fatal("desativacao nao pode trocar a linha da licenca")
	}
	if !deactivated.ValidFrom.Equal(first.ValidFrom) ||
		deactivated.ValidTo == nil || !deactivated.ValidTo.Equal(*first.ValidTo) {
		t.Fatalf("desativacao alterou a vigencia: %v..%v", deactivated.ValidFrom, deactivated.ValidTo)
	}

	second, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: module, CompanyID: companyB, Origin: entitlement.OriginAdmin,
		ValidFrom: &from, ValidTo: &to,
	})
	if err != nil {
		t.Fatalf("desativar A deveria liberar a cota para B: %v", err)
	}

	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: module, CompanyID: companyA, Origin: entitlement.OriginSelfService,
	}); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatalf("reativar A com B ativa deveria exceder a cota, recebido %v", err)
	}

	revoked, err := service.Revoke(ctx, actor, entitlement.TransitionInput{
		ModuleCode: module, CompanyID: companyB, Origin: entitlement.OriginAdmin,
	})
	if err != nil {
		t.Fatalf("revogar B: %v", err)
	}
	if revoked.Status != entitlement.StatusRevoked || revoked.ID != second.ID {
		t.Fatalf("revogacao inesperada: %+v", revoked)
	}
	if _, err := service.Deactivate(ctx, actor, entitlement.TransitionInput{
		ModuleCode: module, CompanyID: companyB, Origin: entitlement.OriginAdmin,
	}); !errors.Is(err, entitlement.ErrInvalidTransition) {
		t.Fatalf("desativar licenca revogada deveria falhar, recebido %v", err)
	}

	reactivated, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: module, CompanyID: companyA, Origin: entitlement.OriginSelfService,
	})
	if err != nil {
		t.Fatalf("reativar A: %v", err)
	}
	if reactivated.ID != first.ID {
		t.Fatal("reativacao deve reaproveitar a mesma linha, sem perder historico")
	}
	if !reactivated.ValidFrom.Equal(first.ValidFrom) ||
		reactivated.ValidTo == nil || !reactivated.ValidTo.Equal(*first.ValidTo) {
		t.Fatalf("reativacao sem datas deveria preservar a vigencia: %v..%v", reactivated.ValidFrom, reactivated.ValidTo)
	}
	if reactivated.Status != entitlement.StatusActive {
		t.Fatalf("status esperado active, recebido %q", reactivated.Status)
	}

	events, err = service.ListEvents(ctx, entitlement.EventFilter{ModuleCode: module, CompanyID: &companyA})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("A deveria ter 3 eventos (ativar, desativar, reativar), recebidos %d", len(events))
	}
	if events[0].Action != entitlement.ActionActivated ||
		events[1].Action != entitlement.ActionDeactivated ||
		events[2].Action != entitlement.ActionActivated {
		t.Fatalf("ordem de auditoria inesperada: %v, %v, %v", events[0].Action, events[1].Action, events[2].Action)
	}

	quota, err := service.ModuleQuota(ctx, module, from)
	if err != nil {
		t.Fatal(err)
	}
	if !quota.Configured || quota.Quota != 1 || quota.InUse != 1 {
		t.Fatalf("cota inesperada: %+v", quota)
	}
	if err := service.SetModuleQuota(ctx, module, 0); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatalf("reduzir cota abaixo das licencas ativas deveria falhar, recebido %v", err)
	}

	for name, statement := range map[string]string{
		"update": `UPDATE entitlement.entitlement_event SET action = 'revoked' WHERE tenant_id = $1`,
		"delete": `DELETE FROM entitlement.entitlement_event WHERE tenant_id = $1`,
	} {
		err := database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, statement, tenantID)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s em entitlement_event deveria ser bloqueado pelo trigger, recebido %v", name, err)
		}
	}

	if quota, err := service.ModuleQuota(ctx, entitlement.ModuleFuneral, from); err != nil || quota.Configured {
		t.Fatalf("modulo sem cota deveria devolver Configured=false: %+v err=%v", quota, err)
	}
	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: entitlement.ModuleFinance, CompanyID: companyA, Origin: entitlement.OriginSelfService,
	}); !errors.Is(err, entitlement.ErrCoreModuleImmutable) {
		t.Fatalf("modulo nucleo deveria ser imutavel, recebido %v", err)
	}
	if _, err := service.Activate(ctx, uuid.Nil, entitlement.ActivateInput{
		ModuleCode: module, CompanyID: companyA, Origin: entitlement.OriginSelfService,
	}); !errors.Is(err, entitlement.ErrActorRequired) {
		t.Fatalf("autor ausente deveria falhar, recebido %v", err)
	}
	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: module, CompanyID: companyA, Origin: "forged",
	}); !errors.Is(err, entitlement.ErrInvalidRequest) {
		t.Fatalf("origem desconhecida deveria falhar, recebido %v", err)
	}

	otherTenant := uuid.New()
	ctxOther := db.WithTenant(context.Background(), otherTenant)
	otherEvents, err := service.ListEvents(ctxOther, entitlement.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(otherEvents) != 0 {
		t.Fatalf("RLS deveria esconder eventos de outro tenant, recebidos %d", len(otherEvents))
	}
	var visible int
	if err := database.WithTenantTx(ctxOther, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM entitlement.entitlement_event WHERE tenant_id = $1`, tenantID).Scan(&visible)
	}); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("RLS vazou %d eventos entre tenants", visible)
	}
}

func TestEntitlementQuotaFailClosedAndSeatUsage(t *testing.T) {
	database, service := newControlService(t)
	tenantID := uuid.New()
	ctx := db.WithTenant(context.Background(), tenantID)
	cleanupTenant(t, database, tenantID)

	actor := uuid.New()
	company := uuid.New()

	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: entitlement.ModuleInventory, CompanyID: company, Origin: entitlement.OriginSelfService,
	}); !errors.Is(err, entitlement.ErrQuotaNotConfigured) {
		t.Fatalf("modulo sem cota provisionada deveria falhar fechado, recebido %v", err)
	}
	if err := service.SetModuleQuota(ctx, entitlement.ModuleFinance, 5); !errors.Is(err, entitlement.ErrCoreModuleImmutable) {
		t.Fatalf("cota de modulo nucleo deveria ser recusada, recebido %v", err)
	}
	if err := service.SetModuleQuota(ctx, entitlement.ModuleInventory, -1); !errors.Is(err, entitlement.ErrInvalidRequest) {
		t.Fatalf("cota negativa deveria ser recusada, recebido %v", err)
	}
	if err := service.SetModuleQuota(ctx, entitlement.ModuleInventory, 3); err != nil {
		t.Fatalf("provisionar cota: %v", err)
	}
	quota, err := service.ModuleQuota(ctx, entitlement.ModuleInventory, date(2026, time.November, 24))
	if err != nil {
		t.Fatal(err)
	}
	if !quota.Configured || quota.Quota != 3 || quota.InUse != 0 {
		t.Fatalf("cota inesperada: %+v", quota)
	}

	userQuota, err := service.UserQuota(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if userQuota.Configured {
		t.Fatalf("sem provisionamento, UserQuota deveria ser Configured=false: %+v", userQuota)
	}
	if err := service.ReportSeatUsage(ctx, 1); !errors.Is(err, entitlement.ErrQuotaNotConfigured) {
		t.Fatalf("reporte sem assentos provisionados deveria falhar, recebido %v", err)
	}
	if err := service.SetUserSeats(ctx, 2); err != nil {
		t.Fatalf("provisionar assentos: %v", err)
	}
	if err := service.ReportSeatUsage(ctx, 2); err != nil {
		t.Fatalf("reporte dentro da cota: %v", err)
	}
	if err := service.ReportSeatUsage(ctx, 3); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatalf("reporte acima da cota deveria falhar, recebido %v", err)
	}
	if err := service.SetUserSeats(ctx, 1); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatalf("reduzir assentos abaixo do uso deveria falhar, recebido %v", err)
	}
	if err := service.SetUserSeats(ctx, 3); err != nil {
		t.Fatalf("ampliar assentos: %v", err)
	}
	userQuota, err = service.UserQuota(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !userQuota.Configured || userQuota.Seats != 3 || userQuota.Used != 2 {
		t.Fatalf("cota de usuarios inesperada: %+v", userQuota)
	}
}

func TestCP06IdempotencyVersionQuotaAuditAndTenantIsolation(t *testing.T) {
	database, service := newControlService(t)
	tenantID := uuid.New()
	ctx := db.WithTenant(context.Background(), tenantID)
	cleanupTenant(t, database, tenantID)
	actor, company := uuid.New(), uuid.New()

	quota, err := service.SetModuleQuotaIdempotent(ctx, actor, "cp06-quota-1", entitlement.ModuleFuneral, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if quota.Version != 1 || quota.Quota != 1 {
		t.Fatalf("quota inicial inesperada: %+v", quota)
	}
	replayedQuota, err := service.SetModuleQuotaIdempotent(ctx, actor, "cp06-quota-1", entitlement.ModuleFuneral, 1, 0)
	if err != nil || replayedQuota.Version != quota.Version {
		t.Fatalf("replay de quota: %+v err=%v", replayedQuota, err)
	}
	if _, err := service.SetModuleQuotaIdempotent(ctx, actor, "cp06-quota-1", entitlement.ModuleFuneral, 2, 1); !errors.Is(err, entitlement.ErrIdempotencyConflict) {
		t.Fatalf("reuso de chave deveria conflitar: %v", err)
	}

	from := date(2026, time.December, 1)
	input := entitlement.ActivateInput{ModuleCode: entitlement.ModuleFuneral, CompanyID: company, Origin: entitlement.OriginAdmin, ValidFrom: &from}
	assignment, err := service.ActivateIdempotent(ctx, actor, "cp06-grant-1", input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.ActivateIdempotent(ctx, actor, "cp06-grant-1", input)
	if err != nil || replayed.ID != assignment.ID || replayed.Version != assignment.Version {
		t.Fatalf("replay de concessao: %+v err=%v", replayed, err)
	}

	revoked, err := service.RevokeIdempotent(ctx, actor, "cp06-revoke-1", entitlement.TransitionInput{
		ModuleCode: entitlement.ModuleFuneral, CompanyID: company, Origin: entitlement.OriginAdmin, ExpectedVersion: assignment.Version,
	})
	if err != nil || revoked.Version != assignment.Version+1 {
		t.Fatalf("revogacao: %+v err=%v", revoked, err)
	}
	if _, err := service.RevokeIdempotent(ctx, actor, "cp06-stale-1", entitlement.TransitionInput{
		ModuleCode: entitlement.ModuleFuneral, CompanyID: company, Origin: entitlement.OriginAdmin, ExpectedVersion: assignment.Version,
	}); !errors.Is(err, entitlement.ErrStaleVersion) {
		t.Fatalf("versao obsoleta deveria falhar: %v", err)
	}

	page, err := service.ListEventsPage(ctx, entitlement.EventFilter{ModuleCode: entitlement.ModuleFuneral, CompanyID: &company, Limit: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("auditoria deveria incluir quota, concessao e revogacao: %+v", page.Items)
	}
	other, err := service.ListEventsPage(db.WithTenant(context.Background(), uuid.New()), entitlement.EventFilter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("RLS vazou auditoria CP-06: %d", len(other.Items))
	}
	otherTenant := uuid.New()
	var visibleIdempotency, visibleQuotaEvents int
	if err := database.WithTenantTx(db.WithTenant(context.Background(), otherTenant), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM entitlement.mutation_idempotency WHERE tenant_id = $1`, tenantID).Scan(&visibleIdempotency); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM entitlement.quota_event WHERE tenant_id = $1`, tenantID).Scan(&visibleQuotaEvents)
	}); err != nil {
		t.Fatal(err)
	}
	if visibleIdempotency != 0 || visibleQuotaEvents != 0 {
		t.Fatalf("RLS vazou tabelas CP-06: idempotência=%d quota_events=%d", visibleIdempotency, visibleQuotaEvents)
	}
	for name, statement := range map[string]string{
		"update quota_event": `UPDATE entitlement.quota_event SET new_quota = new_quota WHERE tenant_id = $1`,
		"delete quota_event": `DELETE FROM entitlement.quota_event WHERE tenant_id = $1`,
	} {
		err := database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, statement, tenantID)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s deveria ser bloqueado pelo trigger: %v", name, err)
		}
	}
}

type controlCompanyResolver map[uuid.UUID]uuid.UUID

func (r controlCompanyResolver) CompanyBelongsToTenant(_ context.Context, tenantID, companyID uuid.UUID) (bool, error) {
	owner, ok := r[companyID]
	return ok && owner == tenantID, nil
}

func TestEntitlementServiceCompanyResolver(t *testing.T) {
	database := newControlDatabase(t)
	tenantID := uuid.New()
	ctx := db.WithTenant(context.Background(), tenantID)
	cleanupTenant(t, database, tenantID)

	owned, foreign := uuid.New(), uuid.New()
	service, err := entitlement.NewService(database, entitlement.WithCompanyResolver(
		controlCompanyResolver{owned: tenantID},
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetModuleQuota(ctx, entitlement.ModuleDocuments, 1); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: entitlement.ModuleDocuments, CompanyID: foreign, Origin: entitlement.OriginSelfService,
	}); !errors.Is(err, entitlement.ErrModuleDisabled) {
		t.Fatalf("empresa fora do tenant deveria ser negada, recebido %v", err)
	}
	if _, err := service.Activate(ctx, actor, entitlement.ActivateInput{
		ModuleCode: entitlement.ModuleDocuments, CompanyID: owned, Origin: entitlement.OriginSelfService,
	}); err != nil {
		t.Fatalf("empresa do tenant deveria ser aceita: %v", err)
	}
}
