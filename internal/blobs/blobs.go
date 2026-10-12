// Package blobs stores attachment bytes outside Postgres. Objects are immutable: a key names exact content.
package blobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MaxBytes is the largest attachment accepted.
const MaxBytes int64 = 25 << 20

type Store interface {
	Check(ctx context.Context) error
	Put(ctx context.Context, key, contentType string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
}

type Minio struct {
	client *minio.Client
	bucket string
}

func NewMinio(endpoint, bucket, access, secret, region string) (*Minio, error) {
	parsed, e := url.Parse(endpoint)
	if e != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("MINIO_ENDPOINT must be an http(s) origin without credentials or a path")
	}
	if bucket == "" || access == "" || secret == "" {
		return nil, errors.New("MinIO bucket and credentials are required")
	}
	client, e := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: parsed.Scheme == "https", Region: region, BucketLookup: minio.BucketLookupPath})
	if e != nil {
		return nil, errors.New("invalid MinIO configuration")
	}
	return &Minio{client, bucket}, nil
}

func (s *Minio) Check(ctx context.Context) error {
	exists, e := s.client.BucketExists(ctx, s.bucket)
	if e != nil {
		return errors.New("MinIO bucket is unreachable")
	}
	if !exists {
		return errors.New("MinIO bucket does not exist")
	}
	return nil
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Put writes once. Writing identical bytes again succeeds; different bytes under the same key are refused.
func (s *Minio) Put(ctx context.Context, key, contentType string, data []byte) error {
	sum := checksum(data)
	opts := minio.PutObjectOptions{ContentType: contentType, DisableMultipart: true, UserMetadata: map[string]string{"sha256": sum}}
	opts.SetMatchETagExcept("*")
	_, e := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), opts)
	if e == nil {
		return nil
	}
	if minio.ToErrorResponse(e).Code != "PreconditionFailed" {
		return errors.New("MinIO write failed")
	}
	info, e := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if e != nil {
		return errors.New("MinIO verification failed")
	}
	var existing string
	for k, v := range info.Metadata {
		if strings.EqualFold(k, "X-Amz-Meta-Sha256") && len(v) > 0 {
			existing = v[0]
		}
	}
	if info.Size != int64(len(data)) || existing != sum {
		return errors.New("immutable attachment object has different content")
	}
	return nil
}

func (s *Minio) Get(ctx context.Context, key string) ([]byte, error) {
	object, e := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if e != nil {
		return nil, errors.New("MinIO read failed")
	}
	defer object.Close()
	data, e := io.ReadAll(io.LimitReader(object, MaxBytes+1))
	if e != nil || int64(len(data)) > MaxBytes {
		return nil, errors.New("MinIO object is unreadable or too large")
	}
	return data, nil
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func NewMemory() *Memory { return &Memory{objects: map[string][]byte{}} }

func (m *Memory) Check(context.Context) error { return nil }
func (m *Memory) Put(_ context.Context, key, _ string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.objects[key]; ok && !bytes.Equal(old, data) {
		return errors.New("immutable attachment object has different content")
	}
	m.objects[key] = append([]byte(nil), data...)
	return nil
}
func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), data...), nil
}
