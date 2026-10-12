package blobs

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Runs against a disposable MinIO: set MINIO_TEST_ENDPOINT (host:port), MINIO_TEST_ACCESS and MINIO_TEST_SECRET.
func TestMinioImmutableObjects(t *testing.T) {
	endpoint, access, secret := os.Getenv("MINIO_TEST_ENDPOINT"), os.Getenv("MINIO_TEST_ACCESS"), os.Getenv("MINIO_TEST_SECRET")
	if endpoint == "" {
		t.Skip("MINIO_TEST_ENDPOINT is not set")
	}
	ctx := context.Background()
	raw, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, "")})
	if err != nil {
		t.Fatal(err)
	}
	bucket := "life-attachments-test"
	if ok, _ := raw.BucketExists(ctx, bucket); !ok {
		if err = raw.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewMinio("http://"+endpoint, bucket, access, secret, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = NewMinio("http://"+endpoint, "missing-bucket", access, secret, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	key := "attachments/test/" + t.Name()
	_ = raw.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err = s.Put(ctx, key, "text/plain", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, key, "text/plain", []byte("hello")); err != nil {
		t.Fatalf("identical write must succeed: %v", err)
	}
	if err = s.Put(ctx, key, "text/plain", []byte("HELLO")); err == nil {
		t.Fatal("different bytes under the same key must be refused")
	}
	got, err := s.Get(ctx, key)
	if err != nil || !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("get: %q %v", got, err)
	}
	if _, err = s.Get(ctx, key+"-missing"); err == nil {
		t.Fatal("missing object must error")
	}
	bad, _ := NewMinio("http://"+endpoint, "no-such-bucket", access, secret, "us-east-1")
	if bad.Check(ctx) == nil {
		t.Fatal("a missing bucket must fail Check")
	}
}

func TestNewMinioValidation(t *testing.T) {
	for _, endpoint := range []string{"", "localhost:9000", "ftp://x", "http://user:pw@x", "http://x/path", "http://x?q=1"} {
		if _, err := NewMinio(endpoint, "b", "a", "s", "r"); err == nil {
			t.Errorf("%q should be rejected", endpoint)
		}
	}
	if _, err := NewMinio("http://x:9000", "", "a", "s", "r"); err == nil {
		t.Error("missing bucket should be rejected")
	}
}

func TestMemoryStoreIsImmutable(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if err := m.Put(ctx, "k", "t", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(ctx, "k", "t", []byte("b")); err == nil {
		t.Fatal("overwrite must be refused")
	}
}
