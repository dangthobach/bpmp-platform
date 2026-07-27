#![allow(clippy::result_large_err)]

use std::fmt::Debug;
use std::io;
use std::ops::{Bound, RangeBounds};
use std::sync::{Arc, Mutex};

use bpmp_raft_state_machine::TypeConfig;
use openraft::storage::{LogFlushed, RaftLogStorage};
use openraft::{
    ErrorSubject, ErrorVerb, LogId, LogState, RaftLogId, RaftLogReader, StorageError, Vote,
};
use rocksdb::{DB, Direction, IteratorMode, WriteBatch, WriteOptions};

use crate::rocks::{RAFT_LOG_CF, RAFT_META_CF};

const VOTE_KEY: &[u8] = b"vote";
const COMMITTED_KEY: &[u8] = b"committed";
const LAST_PURGED_KEY: &[u8] = b"last-purged";

/// Persistent `OpenRaft` log, vote and commit metadata for one engine node.
///
/// Application state and consensus storage share a `RocksDB` instance but use
/// distinct column families. Each metadata transition and related log deletion
/// is issued as one synchronous local `WriteBatch`.
#[derive(Clone)]
pub struct RocksDbRaftLogStorage {
    db: Arc<DB>,
    write_lock: Arc<Mutex<()>>,
}

impl RocksDbRaftLogStorage {
    pub(crate) const fn new(db: Arc<DB>, write_lock: Arc<Mutex<()>>) -> Self {
        Self { db, write_lock }
    }

    fn read_meta<T>(
        &self,
        key: &[u8],
        subject: ErrorSubject<u64>,
    ) -> Result<Option<T>, StorageError<u64>>
    where
        T: serde::de::DeserializeOwned,
    {
        let bytes = self
            .db
            .get_cf(self.meta_cf()?, key)
            .map_err(|error| storage_error(subject.clone(), ErrorVerb::Read, error))?;
        bytes
            .map(|value| {
                serde_json::from_slice(&value)
                    .map_err(|error| storage_error(subject, ErrorVerb::Read, error))
            })
            .transpose()
    }

    fn put_meta<T>(
        &self,
        key: &[u8],
        value: &T,
        subject: ErrorSubject<u64>,
    ) -> Result<(), StorageError<u64>>
    where
        T: serde::Serialize,
    {
        let encoded = serde_json::to_vec(value)
            .map_err(|error| storage_error(subject.clone(), ErrorVerb::Write, error))?;
        let _guard = self.write_lock.lock().map_err(|error| {
            storage_error(
                subject.clone(),
                ErrorVerb::Write,
                io::Error::other(error.to_string()),
            )
        })?;
        let mut batch = WriteBatch::default();
        batch.put_cf(self.meta_cf()?, key, encoded);
        self.write_sync(batch, subject)
    }

    fn log_cf(&self) -> Result<&rocksdb::ColumnFamily, StorageError<u64>> {
        self.db.cf_handle(RAFT_LOG_CF).ok_or_else(|| {
            storage_error(
                ErrorSubject::Logs,
                ErrorVerb::Read,
                io::Error::new(io::ErrorKind::NotFound, "raft_log column family is missing"),
            )
        })
    }

    fn meta_cf(&self) -> Result<&rocksdb::ColumnFamily, StorageError<u64>> {
        self.db.cf_handle(RAFT_META_CF).ok_or_else(|| {
            storage_error(
                ErrorSubject::Store,
                ErrorVerb::Read,
                io::Error::new(
                    io::ErrorKind::NotFound,
                    "raft_meta column family is missing",
                ),
            )
        })
    }

    fn write_sync(
        &self,
        batch: WriteBatch,
        subject: ErrorSubject<u64>,
    ) -> Result<(), StorageError<u64>> {
        let mut options = WriteOptions::default();
        options.set_sync(true);
        self.db
            .write_opt(batch, &options)
            .map_err(|error| storage_error(subject, ErrorVerb::Write, error))
    }
}

impl RaftLogReader<TypeConfig> for RocksDbRaftLogStorage {
    async fn try_get_log_entries<RB: RangeBounds<u64> + Clone + Debug>(
        &mut self,
        range: RB,
    ) -> Result<Vec<openraft::Entry<TypeConfig>>, StorageError<u64>> {
        let start = range_start(&range)?;
        let mut entries = Vec::new();
        for item in self.db.iterator_cf(
            self.log_cf()?,
            IteratorMode::From(&start.to_be_bytes(), Direction::Forward),
        ) {
            let (key, value) =
                item.map_err(|error| storage_error(ErrorSubject::Logs, ErrorVerb::Read, error))?;
            let index = decode_log_index(&key)?;
            if beyond_end(&range, index) {
                break;
            }
            entries.push(serde_json::from_slice(&value).map_err(|error| {
                storage_error(ErrorSubject::LogIndex(index), ErrorVerb::Read, error)
            })?);
        }
        Ok(entries)
    }
}

impl RaftLogStorage<TypeConfig> for RocksDbRaftLogStorage {
    type LogReader = Self;

    async fn get_log_state(&mut self) -> Result<LogState<TypeConfig>, StorageError<u64>> {
        let last_purged_log_id = self.read_meta(LAST_PURGED_KEY, ErrorSubject::Logs)?;
        let mut iterator = self.db.iterator_cf(self.log_cf()?, IteratorMode::End);
        let last_log_id = match iterator.next() {
            Some(item) => {
                let (key, value) = item
                    .map_err(|error| storage_error(ErrorSubject::Logs, ErrorVerb::Read, error))?;
                let index = decode_log_index(&key)?;
                let entry: openraft::Entry<TypeConfig> =
                    serde_json::from_slice(&value).map_err(|error| {
                        storage_error(ErrorSubject::LogIndex(index), ErrorVerb::Read, error)
                    })?;
                Some(*entry.get_log_id())
            }
            None => last_purged_log_id,
        };
        Ok(LogState {
            last_purged_log_id,
            last_log_id,
        })
    }

    async fn save_committed(
        &mut self,
        committed: Option<LogId<u64>>,
    ) -> Result<(), StorageError<u64>> {
        self.put_meta(COMMITTED_KEY, &committed, ErrorSubject::Store)
    }

    async fn read_committed(&mut self) -> Result<Option<LogId<u64>>, StorageError<u64>> {
        self.read_meta(COMMITTED_KEY, ErrorSubject::Store)
    }

    async fn save_vote(&mut self, vote: &Vote<u64>) -> Result<(), StorageError<u64>> {
        self.put_meta(VOTE_KEY, vote, ErrorSubject::Vote)
    }

    async fn read_vote(&mut self) -> Result<Option<Vote<u64>>, StorageError<u64>> {
        self.read_meta(VOTE_KEY, ErrorSubject::Vote)
    }

    async fn append<I>(
        &mut self,
        entries: I,
        callback: LogFlushed<TypeConfig>,
    ) -> Result<(), StorageError<u64>>
    where
        I: IntoIterator<Item = openraft::Entry<TypeConfig>>,
    {
        let _guard = self.write_lock.lock().map_err(|error| {
            storage_error(
                ErrorSubject::Logs,
                ErrorVerb::Write,
                io::Error::other(error.to_string()),
            )
        })?;
        let mut batch = WriteBatch::default();
        for entry in entries {
            let log_id = *entry.get_log_id();
            let value = serde_json::to_vec(&entry).map_err(|error| {
                storage_error(ErrorSubject::Log(log_id), ErrorVerb::Write, error)
            })?;
            batch.put_cf(self.log_cf()?, log_id.index.to_be_bytes(), value);
        }
        let mut options = WriteOptions::default();
        options.set_sync(true);
        match self.db.write_opt(batch, &options) {
            Ok(()) => {
                callback.log_io_completed(Ok(()));
                Ok(())
            }
            Err(error) => {
                let message = error.to_string();
                callback.log_io_completed(Err(io::Error::other(message.clone())));
                Err(storage_error(
                    ErrorSubject::Logs,
                    ErrorVerb::Write,
                    io::Error::other(message),
                ))
            }
        }
    }

    async fn truncate(&mut self, log_id: LogId<u64>) -> Result<(), StorageError<u64>> {
        let _guard = self.write_lock.lock().map_err(|error| {
            storage_error(
                ErrorSubject::Log(log_id),
                ErrorVerb::Delete,
                io::Error::other(error.to_string()),
            )
        })?;
        let mut batch = WriteBatch::default();
        delete_log_range(&mut batch, self.log_cf()?, log_id.index, u64::MAX);
        self.write_sync(batch, ErrorSubject::Log(log_id))
    }

    async fn purge(&mut self, log_id: LogId<u64>) -> Result<(), StorageError<u64>> {
        let encoded = serde_json::to_vec(&log_id)
            .map_err(|error| storage_error(ErrorSubject::Log(log_id), ErrorVerb::Write, error))?;
        let _guard = self.write_lock.lock().map_err(|error| {
            storage_error(
                ErrorSubject::Log(log_id),
                ErrorVerb::Delete,
                io::Error::other(error.to_string()),
            )
        })?;
        let mut batch = WriteBatch::default();
        delete_log_range(&mut batch, self.log_cf()?, 0, log_id.index);
        batch.put_cf(self.meta_cf()?, LAST_PURGED_KEY, encoded);
        self.write_sync(batch, ErrorSubject::Log(log_id))
    }

    async fn get_log_reader(&mut self) -> Self::LogReader {
        self.clone()
    }
}

fn range_start<R: RangeBounds<u64>>(range: &R) -> Result<u64, StorageError<u64>> {
    match range.start_bound() {
        Bound::Unbounded => Ok(0),
        Bound::Included(index) => Ok(*index),
        Bound::Excluded(index) => index.checked_add(1).ok_or_else(|| {
            storage_error(
                ErrorSubject::LogIndex(*index),
                ErrorVerb::Seek,
                io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "excluded log range start overflow",
                ),
            )
        }),
    }
}

fn beyond_end<R: RangeBounds<u64>>(range: &R, index: u64) -> bool {
    match range.end_bound() {
        Bound::Unbounded => false,
        Bound::Included(end) => index > *end,
        Bound::Excluded(end) => index >= *end,
    }
}

fn decode_log_index(key: &[u8]) -> Result<u64, StorageError<u64>> {
    let bytes: [u8; 8] = key.try_into().map_err(|_| {
        storage_error(
            ErrorSubject::Logs,
            ErrorVerb::Read,
            io::Error::new(
                io::ErrorKind::InvalidData,
                "raft log key is not eight bytes",
            ),
        )
    })?;
    Ok(u64::from_be_bytes(bytes))
}

fn delete_log_range(
    batch: &mut WriteBatch,
    family: &rocksdb::ColumnFamily,
    first: u64,
    last_inclusive: u64,
) {
    if last_inclusive == u64::MAX {
        batch.delete_range_cf(family, first.to_be_bytes(), u64::MAX.to_be_bytes());
        batch.delete_cf(family, u64::MAX.to_be_bytes());
    } else {
        batch.delete_range_cf(
            family,
            first.to_be_bytes(),
            last_inclusive.saturating_add(1).to_be_bytes(),
        );
    }
}

fn storage_error(
    subject: ErrorSubject<u64>,
    verb: ErrorVerb,
    error: impl std::error::Error,
) -> StorageError<u64> {
    StorageError::from_io_error(subject, verb, io::Error::other(error.to_string()))
}
