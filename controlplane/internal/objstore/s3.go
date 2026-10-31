package objstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config is parsed from s3://bucket/prefix?region=…&endpoint=…
//
//   - endpoint: host[:port] or a URL (http:// disables TLS). Default
//     s3.amazonaws.com.
//   - region: bucket region (set it to avoid a region lookup).
//   - pathStyle=true forces path-style addressing (MinIO and most
//     S3-compatible services).
//
// Credentials come from AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY,
// MINIO_ACCESS_KEY/MINIO_SECRET_KEY, ~/.aws/credentials or the instance's IAM
// role, in that order.
type S3Config struct {
	Bucket    string
	Prefix    string
	Region    string
	Endpoint  string
	Secure    bool
	PathStyle bool
	// PresignEndpoint is written into presigned URLs when sidecars reach
	// the service at another address (host[:port] or URL).
	PresignEndpoint string
	// Creds overrides the credential chain (tests).
	Creds *credentials.Credentials
}

// ParseS3URL parses an s3:// URL.
func ParseS3URL(raw string) (S3Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return S3Config{}, fmt.Errorf("objstore: %w", err)
	}
	if u.Scheme != "s3" || u.Host == "" {
		return S3Config{}, fmt.Errorf("objstore: want s3://bucket/prefix, got %q", raw)
	}
	q := u.Query()
	cfg := S3Config{
		Bucket:    u.Host,
		Prefix:    strings.Trim(u.Path, "/"),
		Region:    q.Get("region"),
		Endpoint:  "s3.amazonaws.com",
		Secure:    true,
		PathStyle: q.Get("pathStyle") == "true",
	}
	if ep := q.Get("endpoint"); ep != "" {
		cfg.Endpoint, cfg.Secure = splitEndpoint(ep, true)
	}
	cfg.PresignEndpoint = q.Get("presignEndpoint")
	return cfg, nil
}

// splitEndpoint turns "http://host:port" into ("host:port", false).
func splitEndpoint(ep string, defSecure bool) (string, bool) {
	secure := defSecure
	switch {
	case strings.HasPrefix(ep, "http://"):
		ep, secure = strings.TrimPrefix(ep, "http://"), false
	case strings.HasPrefix(ep, "https://"):
		ep, secure = strings.TrimPrefix(ep, "https://"), true
	}
	return strings.TrimRight(ep, "/"), secure
}

// S3 stores objects in a bucket and returns presigned GET URLs.
type S3 struct {
	c       *minio.Client
	presign *minio.Client
	cfg     S3Config
}

// NewS3 builds the client; it does not contact the service.
func NewS3(cfg S3Config) (*S3, error) {
	creds := cfg.Creds
	if creds == nil {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.EnvMinio{},
			&credentials.FileAWSCredentials{},
			&credentials.IAM{Client: &http.Client{Transport: http.DefaultTransport}},
		})
	}
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.Secure, Region: cfg.Region, BucketLookup: lookup})
	if err != nil {
		return nil, fmt.Errorf("objstore: s3 client: %w", err)
	}
	s := &S3{c: c, presign: c, cfg: cfg}
	if cfg.PresignEndpoint != "" {
		ep, secure := splitEndpoint(cfg.PresignEndpoint, cfg.Secure)
		if s.presign, err = minio.New(ep, &minio.Options{Creds: creds, Secure: secure, Region: cfg.Region, BucketLookup: lookup}); err != nil {
			return nil, fmt.Errorf("objstore: s3 presign client: %w", err)
		}
	}
	return s, nil
}

func (s *S3) Kind() string { return "s3" }

func (s *S3) key(k string) (string, error) {
	if !ValidKey(k) {
		return "", fmt.Errorf("objstore: invalid key %q", k)
	}
	if s.cfg.Prefix == "" {
		return k, nil
	}
	return path.Join(s.cfg.Prefix, k), nil
}

func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	ct := "application/octet-stream"
	if strings.HasSuffix(key, ".json") {
		ct = "application/json"
	}
	_, err = s.c.PutObject(ctx, s.cfg.Bucket, k, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: ct})
	if err != nil {
		return fmt.Errorf("objstore: put %s: %w", key, err)
	}
	return nil
}

func isNoSuchKey(err error) bool {
	r := minio.ToErrorResponse(err)
	return r.Code == "NoSuchKey" || r.StatusCode == http.StatusNotFound
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	k, err := s.key(key)
	if err != nil {
		return nil, err
	}
	obj, err := s.c.GetObject(ctx, s.cfg.Bucket, k, minio.GetObjectOptions{})
	if err != nil {
		if isNoSuchKey(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer obj.Close()
	b, err := io.ReadAll(obj)
	if err != nil {
		if isNoSuchKey(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objstore: get %s: %w", key, err)
	}
	return b, nil
}

func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	k, err := s.key(key)
	if err != nil {
		return false, err
	}
	_, err = s.c.StatObject(ctx, s.cfg.Bucket, k, minio.StatObjectOptions{})
	if err != nil {
		if isNoSuchKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *S3) URL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	k, err := s.key(key)
	if err != nil {
		return "", err
	}
	u, err := s.presign.PresignedGetObject(ctx, s.cfg.Bucket, k, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("objstore: presign %s: %w", key, err)
	}
	return u.String(), nil
}
