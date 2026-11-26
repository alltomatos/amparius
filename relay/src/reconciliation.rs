use crate::OperationKind;
use serde::{Deserialize, Serialize};

/// Domínio usado para decidir a política de reconciliação de uma operação.
///
/// A classificação é deliberadamente pequena e fechada: dinheiro e fiscal não
/// podem cair numa política genérica de "última escrita vence".
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ReconciliationDomain {
    Master,
    Money,
    Funeral,
    Fiscal,
}

impl ReconciliationDomain {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Master => "master",
            Self::Money => "money",
            Self::Funeral => "funeral",
            Self::Fiscal => "fiscal",
        }
    }
}

pub fn domain_for(kind: OperationKind) -> ReconciliationDomain {
    match kind {
        OperationKind::MasterUpsert => ReconciliationDomain::Master,
        OperationKind::FiscalEmission => ReconciliationDomain::Fiscal,
        OperationKind::ContractCreate
        | OperationKind::CashOpen
        | OperationKind::CashMovement
        | OperationKind::CashClose => ReconciliationDomain::Money,
        OperationKind::FuneralJourneyCreate
        | OperationKind::FuneralServiceOrderAdvance
        | OperationKind::FuneralServiceOrderPause
        | OperationKind::FuneralServiceOrderComplete => ReconciliationDomain::Funeral,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_operation_has_an_explicit_reconciliation_domain() {
        assert_eq!(
            domain_for(OperationKind::MasterUpsert),
            ReconciliationDomain::Master
        );
        assert_eq!(
            domain_for(OperationKind::ContractCreate),
            ReconciliationDomain::Money
        );
        assert_eq!(
            domain_for(OperationKind::CashMovement),
            ReconciliationDomain::Money
        );
        assert_eq!(
            domain_for(OperationKind::FiscalEmission),
            ReconciliationDomain::Fiscal
        );
    }
}
