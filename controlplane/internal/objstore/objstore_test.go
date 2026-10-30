package objstore

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestFS(t *testing.T) {
	ctx := context.Background()
	sg := Signer{Key: []byte("k"), BaseURL: "http://x/api/v1"}
	fs, err := NewFS(t.TempDir(), sg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Get(ctx, "blobs/sha256/aa"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	if err := fs.Put(ctx, "blobs/sha256/aa", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	b, err := fs.Get(ctx, "blobs/sha256/aa")
	if err != nil || string(b) != "hi" {
		t.Fatal(err, string(b))
	}
	if ok, _ := fs.Exists(ctx, "blobs/sha256/aa"); !ok {
		t.Fatal("exists")
	}
	if err := fs.Put(ctx, "../evil", nil); err == nil {
		t.Fatal("path traversal accepted")
	}
	u, err := fs.URL(ctx, "blobs/sha256/aa", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	if !strings.HasSuffix(pu.Path, "/api/v1/sidecar/v1/blobs/aa") {
		t.Fatalf("url %s", u)
	}
	exp, _ := strconv.ParseInt(pu.Query().Get("exp"), 10, 64)
	if !sg.Verify("aa", pu.Query().Get("sig"), exp) {
		t.Fatal("signature does not verify")
	}
	if sg.Verify("bb", pu.Query().Get("sig"), exp) {
		t.Fatal("signature verifies for another blob")
	}
	if sg.Verify("aa", pu.Query().Get("sig"), time.Now().Add(-time.Minute).Unix()) {
		t.Fatal("expired signature verifies")
	}
	if _, err := fs.URL(ctx, "envelopes/a/b/1.pb", time.Minute); err == nil {
		t.Fatal("non-blob URL")
	}
}

func TestParseS3URL(t *testing.T) {
	c, err := ParseS3URL("s3://bucket/some/prefix?region=eu-west-1&endpoint=http://localhost:9000&pathStyle=true")
	if err != nil {
		t.Fatal(err)
	}
	if c.Bucket != "bucket" || c.Prefix != "some/prefix" || c.Region != "eu-west-1" || c.Endpoint != "localhost:9000" || c.Secure || !c.PathStyle {
		t.Fatalf("%+v", c)
	}
	c, _ = ParseS3URL("s3://b")
	if c.Endpoint != "s3.amazonaws.com" || !c.Secure || c.Prefix != "" {
		t.Fatalf("%+v", c)
	}
	if _, err := ParseS3URL("s3:///nobucket"); err == nil {
		t.Fatal("accepted empty bucket")
	}
}

func TestS3PresignOffline(t *testing.T) {
	cfg, _ := ParseS3URL("s3://bucket/pre?region=us-east-1&endpoint=http://127.0.0.1:1&pathStyle=true")
	cfg.Creds = credentials.NewStaticV4("AK", "SK", "")
	s, err := NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.URL(context.Background(), "blobs/sha256/abc", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "/bucket/pre/blobs/sha256/abc") || !strings.Contains(u, "X-Amz-Signature=") {
		t.Fatalf("presigned url %s", u)
	}
}

// TestS3Integration runs against a real S3-compatible service when
// GEARBOX_TEST_S3_URL is set (e.g. s3://test/gbx?endpoint=http://localhost:9000&region=us-east-1&pathStyle=true).
func TestS3Integration(t *testing.T) {
	raw := os.Getenv("GEARBOX_TEST_S3_URL")
	if raw == "" {
		t.Skip("GEARBOX_TEST_S3_URL not set")
	}
	cfg, err := ParseS3URL(raw)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "blobs/sha256/test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := s.Put(ctx, key, []byte("x")); err != nil {
		t.Fatal(err)
	}
	b, err := s.Get(ctx, key)
	if err != nil || string(b) != "x" {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, key+"-missing"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
}
