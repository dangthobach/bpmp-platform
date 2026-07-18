//! `MaterializedPath` value object — mirrors PostgreSQL `ltree` semantics.
//!
//! Labels are dot-separated, ASCII `[a-z0-9_]`, max length 64 per segment.
//! Construction validates; once built the value is immutable.

use serde::{Deserialize, Serialize};

use crate::domain::errors::DomainError;

#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(transparent)]
pub struct MaterializedPath(String);

impl MaterializedPath {
    pub fn new(raw: impl Into<String>) -> Result<Self, DomainError> {
        let s = raw.into();
        if s.is_empty() {
            return Err(DomainError::Invariant("path must not be empty"));
        }
        for seg in s.split('.') {
            if seg.is_empty() || seg.len() > 64 {
                return Err(DomainError::Invariant("path segment invalid length"));
            }
            if !seg
                .bytes()
                .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'_')
            {
                return Err(DomainError::Invariant("path segment must be [a-z0-9_]"));
            }
        }
        Ok(Self(s))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub fn depth(&self) -> u16 {
        self.0.split('.').count() as u16
    }

    /// Returns `true` if `self` is an ancestor of `other` (or equal).
    pub fn is_ancestor_of(&self, other: &MaterializedPath) -> bool {
        other.0 == self.0
            || (other.0.starts_with(&self.0) && other.0.as_bytes().get(self.0.len()) == Some(&b'.'))
    }

    /// Append a child label, returning a new path.
    pub fn child(&self, label: &str) -> Result<Self, DomainError> {
        Self::new(format!("{}.{}", self.0, label))
    }

    /// Replace the prefix `from` with `to`. Used when moving a subtree.
    pub fn reparent(
        &self,
        from: &MaterializedPath,
        to: &MaterializedPath,
    ) -> Result<Self, DomainError> {
        if !from.is_ancestor_of(self) {
            return Err(DomainError::Invariant("path is not under `from`"));
        }
        let tail = &self.0[from.0.len()..]; // either "" or ".rest"
        Self::new(format!("{}{}", to.0, tail))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_invalid_segment() {
        assert!(MaterializedPath::new("Group.x").is_err());
        assert!(MaterializedPath::new("").is_err());
        assert!(MaterializedPath::new("a..b").is_err());
        assert!(MaterializedPath::new("a.b!").is_err());
    }

    #[test]
    fn ancestor_check() {
        let a = MaterializedPath::new("g.s1").unwrap();
        let b = MaterializedPath::new("g.s1.b1").unwrap();
        let c = MaterializedPath::new("g.s2").unwrap();
        assert!(a.is_ancestor_of(&b));
        assert!(a.is_ancestor_of(&a));
        assert!(!a.is_ancestor_of(&c));
        // partial-prefix trap:
        let d = MaterializedPath::new("g.s10").unwrap();
        assert!(!a.is_ancestor_of(&d));
    }

    #[test]
    fn reparent_preserves_tail() {
        let from = MaterializedPath::new("g.s1").unwrap();
        let to = MaterializedPath::new("g.s2").unwrap();
        let node = MaterializedPath::new("g.s1.b1.d1").unwrap();
        let moved = node.reparent(&from, &to).unwrap();
        assert_eq!(moved.as_str(), "g.s2.b1.d1");
    }
}
