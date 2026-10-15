use std::io::{self, SeekFrom, Write};
use std::path::Path;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};

use tempfile::NamedTempFile;
use tokio::io::{AsyncRead, AsyncSeek, AsyncWrite, ReadBuf};

/// File-backed transfer buffer. The byte budget also bounds sparse seeks.
#[derive(Debug)]
pub struct SnapshotFile {
    file: tokio::fs::File,
    backing: Arc<NamedTempFile>,
    limit: u64,
    position: u64,
}

impl SnapshotFile {
    pub(crate) fn create(directory: &Path, limit: u64) -> io::Result<Self> {
        Self::open(Arc::new(NamedTempFile::new_in(directory)?), limit)
    }

    pub(crate) fn open(backing: Arc<NamedTempFile>, limit: u64) -> io::Result<Self> {
        if backing.as_file().metadata()?.len() > limit {
            return Err(oversized());
        }
        Ok(Self {
            file: tokio::fs::File::from_std(backing.reopen()?),
            backing,
            limit,
            position: 0,
        })
    }

    pub(crate) fn backing(&self) -> Arc<NamedTempFile> {
        self.backing.clone()
    }
}

fn oversized() -> io::Error {
    io::Error::new(
        io::ErrorKind::InvalidData,
        "Raft snapshot byte budget exceeded",
    )
}

impl AsyncRead for SnapshotFile {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let before = buf.filled().len();
        let result = Pin::new(&mut self.file).poll_read(cx, buf);
        if let Poll::Ready(Ok(())) = result {
            self.position += (buf.filled().len() - before) as u64;
        }
        result
    }
}

impl AsyncWrite for SnapshotFile {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        if self
            .position
            .checked_add(buf.len() as u64)
            .is_none_or(|end| end > self.limit)
        {
            return Poll::Ready(Err(oversized()));
        }
        let result = Pin::new(&mut self.file).poll_write(cx, buf);
        if let Poll::Ready(Ok(written)) = result {
            self.position += written as u64;
        }
        result
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.file).poll_flush(cx)
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.file).poll_shutdown(cx)
    }
}

impl AsyncSeek for SnapshotFile {
    fn start_seek(mut self: Pin<&mut Self>, position: SeekFrom) -> io::Result<()> {
        let end = match position {
            SeekFrom::Start(end) => Some(end),
            SeekFrom::Current(delta) => self.position.checked_add_signed(delta),
            // OpenRaft discovers snapshot length with SeekFrom::End(0).
            SeekFrom::End(delta) => self
                .backing
                .as_file()
                .metadata()?
                .len()
                .checked_add_signed(delta),
        };
        if end.is_none_or(|end| end > self.limit) {
            return Err(oversized());
        }
        Pin::new(&mut self.file).start_seek(position)
    }

    fn poll_complete(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<u64>> {
        let result = Pin::new(&mut self.file).poll_complete(cx);
        if let Poll::Ready(Ok(position)) = result {
            self.position = position;
        }
        result
    }
}

pub(crate) struct BoundedWriter<W> {
    pub(crate) inner: W,
    pub(crate) remaining: u64,
}

impl<W: Write> Write for BoundedWriter<W> {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        if bytes.len() as u64 > self.remaining {
            return Err(oversized());
        }
        let written = self.inner.write(bytes)?;
        self.remaining -= written as u64;
        Ok(written)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::{AsyncReadExt as _, AsyncSeekExt as _, AsyncWriteExt as _};

    #[tokio::test]
    async fn snapshot_budget_rejects_writes_and_sparse_seek_without_partial_growth() {
        let directory = tempfile::tempdir().unwrap();
        let mut snapshot = SnapshotFile::create(directory.path(), 4).unwrap();
        snapshot.write_all(b"1234").await.unwrap();
        snapshot.flush().await.unwrap();
        assert!(snapshot.write_all(b"5").await.is_err());
        assert!(snapshot.seek(SeekFrom::Start(5)).await.is_err());
        assert_eq!(snapshot.backing.as_file().metadata().unwrap().len(), 4);
        snapshot.rewind().await.unwrap();
        let mut bytes = Vec::new();
        snapshot.read_to_end(&mut bytes).await.unwrap();
        assert_eq!(bytes, b"1234");
    }

    #[tokio::test]
    async fn readers_have_independent_offsets_and_aborted_transfers_are_removed() {
        let directory = tempfile::tempdir().unwrap();
        let mut snapshot = SnapshotFile::create(directory.path(), 64).unwrap();
        snapshot.write_all(b"snapshot").await.unwrap();
        snapshot.flush().await.unwrap();
        let path = snapshot.backing.path().to_owned();
        let mut reader = SnapshotFile::open(snapshot.backing(), 64).unwrap();
        snapshot.rewind().await.unwrap();
        let mut bytes = [0; 4];
        snapshot.read_exact(&mut bytes).await.unwrap();
        reader.read_exact(&mut bytes).await.unwrap();
        assert_eq!(&bytes, b"snap");
        drop(snapshot);
        assert!(path.exists());
        drop(reader);
        assert!(!path.exists());
    }

    #[test]
    fn serialization_budget_bounds_actual_wire_bytes() {
        let mut bytes = Vec::new();
        let mut writer = BoundedWriter {
            inner: &mut bytes,
            remaining: 3,
        };
        assert!(serde_json::to_writer(&mut writer, &vec![255_u8]).is_err());
        assert!(bytes.len() <= 3);
    }
}
