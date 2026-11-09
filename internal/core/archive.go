package core

import (
	"context"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tildoc/tildoc/internal/blob"
)

// Sealer wraps an export stream, e.g. to encrypt it. nil means plain output.
type Sealer interface {
	Seal(w io.Writer) (io.WriteCloser, error)
}

// Opener unwraps an import stream sealed by a Sealer. nil means plain input.
type Opener interface {
	Open(r io.Reader) (io.Reader, error)
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func seal(w io.Writer, s Sealer) (io.WriteCloser, error) {
	if s == nil {
		return nopWriteCloser{w}, nil
	}
	return s.Seal(w)
}

func open(r io.Reader, o Opener) (io.Reader, error) {
	if o == nil {
		return r, nil
	}
	return o.Open(r)
}

// blobLockKey is the advisory lock that keeps blob garbage collection from
// deleting files while exports stream them (shared) or GC runs (exclusive).
const blobLockKey = 0x646f63746f6e6901

// holdBlobs keeps GC away until release is called.
func (s *Service) holdBlobs(ctx context.Context) (release func(), err error) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock_shared($1)`, int64(blobLockKey)); err != nil {
		conn.Release()
		return nil, err
	}
	return func() {
		conn.Exec(context.Background(), `SELECT pg_advisory_unlock_shared($1)`, int64(blobLockKey)) //nolint:errcheck // the lock ends with the connection anyway
		conn.Release()
	}, nil
}

// blobClaim writes a staged blob into the store from inside the transaction
// that records it. The transaction holds the shared blob lock, so garbage
// collection (exclusive) can never delete the content between the file
// being written and the database referencing it. Safe for retried
// transactions; call discardUnused after the transaction.
type blobClaim struct {
	s                *Service
	st               *blob.Staged
	committed, isNew bool
}

func (s *Service) claim(st *blob.Staged) *blobClaim { return &blobClaim{s: s, st: st} }

func (c *blobClaim) commit(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, int64(blobLockKey)); err != nil {
		return err
	}
	if c.committed { // a retried transaction: the file was moved into place before
		if !c.s.Blobs.Exists(c.st.SHA256) {
			return errors.New("blob disappeared between transaction attempts")
		}
		return nil
	}
	n, err := c.s.Blobs.Commit(c.st)
	if err != nil {
		return err
	}
	c.committed, c.isNew = true, n
	return nil
}

// discardUnused removes the staged file if the transaction never committed it.
func (c *blobClaim) discardUnused() {
	if !c.committed {
		c.s.Blobs.Discard(c.st)
	}
}

// tryBlobGC takes the exclusive GC lock if no export is running.
func (s *Service) tryBlobGC(ctx context.Context) (*pgxpool.Conn, bool, error) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(blobLockKey)).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false, err
	}
	return conn, true, nil
}

// snapshot runs fn in a read-only REPEATABLE READ transaction: every query
// sees the same consistent state of the database.
func (s *Service) snapshot(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
