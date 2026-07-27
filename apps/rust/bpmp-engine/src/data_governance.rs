use thiserror::Error;

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum PayloadAvailability {
    Available,
    Masked,
    KeyDestroyed,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ErasureState {
    pub tenant_id: String,
    pub instance_id: String,
    pub terminal: bool,
    pub execution_fenced: bool,
    pub non_pii_tombstone: String,
    pub payload: PayloadAvailability,
    pub governance_obligations_committed: bool,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum PiiReadOutcome {
    Available,
    Masked,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum ErasureDecision {
    DestroyKey,
    AlreadyDestroyed,
}

impl ErasureState {
    /// Returns a privacy-preserving read result without exposing destroyed data.
    pub const fn read_pii(&self) -> PiiReadOutcome {
        match self.payload {
            PayloadAvailability::Available => PiiReadOutcome::Available,
            PayloadAvailability::Masked | PayloadAvailability::KeyDestroyed => {
                PiiReadOutcome::Masked
            }
        }
    }

    /// Marks logical erasure while retaining a non-PII structural tombstone.
    ///
    /// # Errors
    ///
    /// Fails when scope or tombstone metadata is absent.
    pub fn mask(&mut self) -> Result<(), ErasureError> {
        self.validate()?;
        if self.payload == PayloadAvailability::Available {
            self.payload = PayloadAvailability::Masked;
        }
        Ok(())
    }

    /// Decides whether cryptographic erasure may destroy the payload key.
    ///
    /// Active instances must first be durably fenced. Governance obligations
    /// must be committed in either the normal terminal or urgent path.
    pub fn decide_key_destruction(&self) -> Result<ErasureDecision, ErasureError> {
        self.validate()?;
        if self.payload == PayloadAvailability::KeyDestroyed {
            return Ok(ErasureDecision::AlreadyDestroyed);
        }
        if !self.governance_obligations_committed {
            return Err(ErasureError::GovernanceNotCommitted);
        }
        if !self.terminal && !self.execution_fenced {
            return Err(ErasureError::ActiveInstanceNotFenced);
        }
        Ok(ErasureDecision::DestroyKey)
    }

    /// Applies the durable result of key destruction.
    ///
    /// # Errors
    ///
    /// Fails closed unless `decide_key_destruction` permits the transition.
    pub fn key_destroyed(&mut self) -> Result<(), ErasureError> {
        let _ = self.decide_key_destruction()?;
        self.payload = PayloadAvailability::KeyDestroyed;
        Ok(())
    }

    /// Missing payloads are never passed to `decide`/`evolve`.
    pub const fn may_execute(&self) -> bool {
        matches!(self.payload, PayloadAvailability::Available)
            && !self.execution_fenced
            && !self.terminal
    }

    fn validate(&self) -> Result<(), ErasureError> {
        if self.tenant_id.trim().is_empty()
            || self.instance_id.trim().is_empty()
            || self.non_pii_tombstone.trim().is_empty()
        {
            Err(ErasureError::InvalidState)
        } else {
            Ok(())
        }
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum ErasureError {
    #[error("erasure state is invalid")]
    InvalidState,
    #[error("governance obligations have not been committed")]
    GovernanceNotCommitted,
    #[error("active workflow instance must be durably fenced before key destruction")]
    ActiveInstanceNotFenced,
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::*;

    fn state(terminal: bool, fenced: bool, committed: bool) -> ErasureState {
        ErasureState {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-a".into(),
            terminal,
            execution_fenced: fenced,
            non_pii_tombstone: "workflow/order/v1/events=12".into(),
            payload: PayloadAvailability::Available,
            governance_obligations_committed: committed,
        }
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 48: Erasure masks PII and preserves replay structure
        #[test]
        fn masking_never_removes_structural_tombstone(
            terminal in any::<bool>(),
            fenced in any::<bool>(),
            committed in any::<bool>(),
        ) {
            let mut value = state(terminal, fenced, committed);
            let tombstone = value.non_pii_tombstone.clone();
            value.mask().unwrap();
            prop_assert_eq!(value.read_pii(), PiiReadOutcome::Masked);
            prop_assert_eq!(value.non_pii_tombstone, tombstone);
        }

        // Feature: rust-bpm-platform, Property 49: Key destruction requires terminal or fenced governance state
        #[test]
        fn key_destruction_is_fenced_and_execution_fails_closed(
            terminal in any::<bool>(),
            fenced in any::<bool>(),
            committed in any::<bool>(),
        ) {
            let mut value = state(terminal, fenced, committed);
            let permitted = committed && (terminal || fenced);
            prop_assert_eq!(value.key_destroyed().is_ok(), permitted);
            if permitted {
                prop_assert!(!value.may_execute());
                prop_assert_eq!(value.read_pii(), PiiReadOutcome::Masked);
            }
        }
    }
}
