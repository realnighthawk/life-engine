package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/realnighthawk/life-engine/internal/blobs"
)

var attachmentTypes = []string{"image/jpeg", "image/png", "image/heic", "image/webp", "audio/mp4", "audio/mpeg", "audio/x-m4a", "audio/aac", "audio/wav", "application/pdf", "text/plain"}

type Attachment struct {
	ID          string     `json:"id"`
	OwnerType   string     `json:"owner_type" jsonschema:"journal_entry or note"`
	OwnerID     string     `json:"owner_id"`
	Filename    string     `json:"filename"`
	ContentType string     `json:"content_type"`
	SizeBytes   int64      `json:"size_bytes"`
	SHA256      string     `json:"sha256,omitempty"`
	State       string     `json:"state" jsonschema:"pending until the bytes are uploaded, then ready"`
	UploadPath  string     `json:"upload_path,omitempty" jsonschema:"PUT the exact bytes here (Content-Type must match) while pending"`
	ContentPath string     `json:"content_path,omitempty" jsonschema:"GET the bytes here once ready"`
	Version     int64      `json:"version"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
type AttachmentInitInput struct {
	Meta
	ID          string `json:"id,omitempty"`
	OwnerType   string `json:"owner_type" jsonschema:"journal_entry or note"`
	OwnerID     string `json:"owner_id"`
	Filename    string `json:"filename" jsonschema:"1-200 characters"`
	ContentType string `json:"content_type" jsonschema:"image/jpeg, image/png, image/heic, image/webp, audio/mp4, audio/mpeg, audio/x-m4a, audio/aac, audio/wav, application/pdf or text/plain"`
	SizeBytes   int64  `json:"size_bytes" jsonschema:"exact size of the file, 1 byte to 25 MiB"`
}

const attachmentCols = `id::text,owner_type,owner_id::text,filename,content_type,size_bytes,coalesce(sha256,''),state,version,deleted_at,created_at,updated_at`

func scanAttachment(row scanner) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.OwnerType, &a.OwnerID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.State, &a.Version, &a.DeletedAt, &a.CreatedAt, &a.UpdatedAt)
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	utcPtr(a.DeletedAt)
	a.paths()
	return a, err
}

func (a *Attachment) paths() {
	a.UploadPath, a.ContentPath = "", ""
	if a.State == "pending" {
		a.UploadPath = "/api/v1/attachments/" + a.ID + "/content"
	} else {
		a.ContentPath = "/api/v1/attachments/" + a.ID + "/content"
	}
}

func (u *unit) getAttachment(ctx context.Context, key string, forUpdate bool) (Attachment, error) {
	k, e := id(key)
	if e != nil {
		return Attachment{}, e
	}
	q := "SELECT " + attachmentCols + " FROM life.attachments WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanAttachment(u.tx.QueryRow(ctx, q, k))
}

// attachmentsOf lists the ready, undeleted attachments of a record.
func (u *unit) attachmentsOf(ctx context.Context, ownerType, ownerID string) ([]Attachment, error) {
	rows, e := u.tx.Query(ctx, "SELECT "+attachmentCols+" FROM life.attachments WHERE owner_type=$1 AND owner_id=$2 AND state='ready' AND deleted_at IS NULL ORDER BY created_at, id", ownerType, ownerID)
	if e != nil {
		return nil, e
	}
	list, e := collect(rows, scanAttachment)
	if list == nil {
		list = []Attachment{}
	}
	return list, e
}

func (s *Service) storageError() error {
	if s.blobs == nil {
		return &Error{"busy", "Attachment storage is not configured"}
	}
	return nil
}

func (s *Service) initAttachment(ctx context.Context, u *unit, in AttachmentInitInput) (Attachment, error) {
	if e := s.storageError(); e != nil {
		return Attachment{}, e
	}
	key, e := createID(in.ID)
	if e != nil {
		return Attachment{}, e
	}
	owner, e := id(in.OwnerID)
	if e != nil {
		return Attachment{}, e
	}
	table := map[string]string{"journal_entry": "journal_entries", "note": "notes"}[in.OwnerType]
	if table == "" {
		return Attachment{}, invalid("owner_type must be journal_entry or note")
	}
	var found bool
	if e = u.tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM life."+table+" WHERE id=$1 AND deleted_at IS NULL)", owner).Scan(&found); e != nil {
		return Attachment{}, e
	}
	if !found {
		return Attachment{}, invalid(in.OwnerType + " does not exist")
	}
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.Filename), `\`, "/"))
	if name == "" || name == "." || name == "/" || len(name) > 200 {
		return Attachment{}, invalid("filename must be 1-200 characters")
	}
	if !oneOf(in.ContentType, attachmentTypes...) {
		return Attachment{}, invalid("content_type is not an accepted attachment type")
	}
	if in.SizeBytes < 1 || in.SizeBytes > blobs.MaxBytes {
		return Attachment{}, invalid("size_bytes must be 1 byte to 25 MiB")
	}
	var count int
	if e = u.tx.QueryRow(ctx, `SELECT count(*) FROM life.attachments WHERE owner_type=$1 AND owner_id=$2 AND deleted_at IS NULL`, in.OwnerType, owner).Scan(&count); e != nil {
		return Attachment{}, e
	}
	if count >= 20 {
		return Attachment{}, invalid("a record can have at most 20 attachments")
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.attachments(id,owner_type,owner_id,filename,content_type,size_bytes) VALUES($1,$2,$3,$4,$5,$6)`, key, in.OwnerType, owner, name, in.ContentType, in.SizeBytes); e != nil {
		return Attachment{}, e
	}
	a, e := u.getAttachment(ctx, key, false)
	if e != nil {
		return a, e
	}
	return a, u.audit(ctx, "attachment", key, nil, a)
}

func lifecycleAttachment(ctx context.Context, u *unit, in EditInput, restore bool) (Attachment, error) {
	old, e := u.getAttachment(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Attachment is not deleted")
		}
		return old, conflict("Attachment is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.attachments SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	a, e := u.getAttachment(ctx, old.ID, false)
	if e != nil {
		return a, e
	}
	return a, u.audit(ctx, "attachment", a.ID, old, a)
}

// write runs one transaction outside the operation registry (the binary upload), under the same write lock.
func (s *Service) write(ctx context.Context, name string, fn func(*unit) error) error {
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(91438)"); e != nil {
		return e
	}
	if e = fn(&unit{tx: tx, actor: "router", operation: name}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func attachmentKey(a Attachment, sum string) string { return "attachments/" + a.ID + "/" + sum }

// Upload receives the bytes reserved by attachments_upload_init. The declared size and type must match exactly.
// Repeating the same upload succeeds; different bytes for a finished attachment are a conflict.
func (s *Service) Upload(ctx context.Context, key, contentType string, body io.Reader) (Attachment, error) {
	if e := s.storageError(); e != nil {
		return Attachment{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var a Attachment
	e := s.write(ctx, "attachments_upload", func(u *unit) (err error) { a, err = u.getAttachment(ctx, key, false); return })
	if e != nil {
		return a, e
	}
	if a.DeletedAt != nil {
		return a, conflict("Restore the attachment before uploading")
	}
	if mt := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]); mt != a.ContentType {
		return a, invalid("Content-Type must be " + a.ContentType)
	}
	data, e := io.ReadAll(io.LimitReader(body, blobs.MaxBytes+1))
	if e != nil || int64(len(data)) > blobs.MaxBytes {
		return a, &Error{"too_large", "Attachment exceeds 25 MiB"}
	}
	if int64(len(data)) != a.SizeBytes {
		return a, invalid("body size does not match the size declared when the upload was reserved")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if a.State == "ready" {
		if a.SHA256 != hash {
			return a, conflict("Attachment already has different content")
		}
		return a, nil
	}
	if e = s.blobs.Put(ctx, attachmentKey(a, hash), a.ContentType, data); e != nil {
		return a, &Error{"busy", "Attachment storage is unavailable; retry this upload"}
	}
	var out Attachment
	e = s.write(ctx, "attachments_upload", func(u *unit) error {
		cur, err := u.getAttachment(ctx, key, true)
		if err != nil {
			return err
		}
		if cur.State == "ready" {
			if cur.SHA256 != hash {
				return conflict("Attachment already has different content")
			}
			out = cur
			return nil
		}
		if _, err = u.tx.Exec(ctx, `UPDATE life.attachments SET state='ready',sha256=$2,version=version+1,updated_at=now() WHERE id=$1`, cur.ID, hash); err != nil {
			return err
		}
		if out, err = u.getAttachment(ctx, key, false); err != nil {
			return err
		}
		return u.audit(ctx, "attachment", out.ID, cur, out)
	})
	return out, e
}

// Download returns a ready attachment's bytes.
func (s *Service) Download(ctx context.Context, key string) (Attachment, []byte, error) {
	if e := s.storageError(); e != nil {
		return Attachment{}, nil, e
	}
	var a Attachment
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		return a, nil, e
	}
	defer tx.Rollback(context.Background())
	if a, e = (&unit{tx: tx}).getAttachment(ctx, key, false); e != nil {
		return a, nil, e
	}
	if a.DeletedAt != nil || a.State != "ready" {
		return a, nil, notFound()
	}
	data, e := s.blobs.Get(ctx, attachmentKey(a, a.SHA256))
	if e != nil {
		return a, nil, &Error{"busy", "Attachment storage is unavailable; retry"}
	}
	sum := sha256.Sum256(data)
	if !bytes.Equal([]byte(hex.EncodeToString(sum[:])), []byte(a.SHA256)) {
		return a, nil, &Error{"internal", "Stored attachment failed its checksum"}
	}
	return a, data, nil
}

// AttachmentsConfigured reports whether object storage is available.
func (s *Service) AttachmentsConfigured() bool { return s.blobs != nil }

// StorageReady is nil when attachments are not configured or storage answers.
func (s *Service) StorageReady(ctx context.Context) error {
	if s.blobs == nil {
		return nil
	}
	return s.blobs.Check(ctx)
}
