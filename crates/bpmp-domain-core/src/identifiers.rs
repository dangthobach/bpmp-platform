use std::fmt::{self, Display};

use thiserror::Error;

#[derive(Debug, Error, Clone, Eq, PartialEq)]
#[error("{kind} must not be empty")]
pub struct IdentifierError {
    kind: &'static str,
}

macro_rules! identifier {
    ($name:ident) => {
        #[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd, Hash)]
        pub struct $name(String);

        impl $name {
            /// Creates a validated identifier.
            ///
            /// # Errors
            ///
            /// Returns an error when the value is empty or contains only whitespace.
            pub fn new(value: impl Into<String>) -> Result<Self, IdentifierError> {
                let value = value.into();
                if value.trim().is_empty() {
                    return Err(IdentifierError {
                        kind: stringify!($name),
                    });
                }
                Ok(Self(value))
            }

            pub fn as_str(&self) -> &str {
                &self.0
            }
        }

        impl Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(&self.0)
            }
        }
    };
}

identifier!(ActorId);
identifier!(CommandId);
identifier!(ConfigId);
identifier!(ConfigVersion);
identifier!(CorrelationId);
identifier!(IdempotencyKey);
identifier!(InstanceId);
identifier!(KeyScope);
identifier!(NodeId);
identifier!(PolicyVersion);
identifier!(ScopeInstanceId);
identifier!(TaskType);
identifier!(TenantId);
identifier!(WorkflowType);
identifier!(WorkflowVersion);
