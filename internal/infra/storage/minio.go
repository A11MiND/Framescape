// Package storage wires MinIO (S3-compatible) for asset materialize (PRD
// F2.2/R3): provider URLs (MiniMax's 24h-expiring links) must never be
// stored as the asset's public_url — the executor downloads the bytes and
// re-uploads them here immediately, so "产物是自己域名" (DEV_PLAN.md §7 W3
// acceptance) holds and nothing goes dead after 24 hours.
package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint        string // "127.0.0.1:9000", no scheme
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	Bucket          string
	PublicBaseURL   string // e.g. "http://127.0.0.1:9000/aigc-assets" — what asset.PublicURL is built from
}

type Store struct {
	client *minio.Client
	cfg    Config
}

func New(ctx context.Context, cfg Config) (*Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("construct minio client: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket %q: %w", cfg.Bucket, err)
		}
		if err := client.SetBucketPolicy(ctx, cfg.Bucket, publicReadPolicy(cfg.Bucket)); err != nil {
			return nil, fmt.Errorf("set public-read policy on %q: %w", cfg.Bucket, err)
		}
	}

	return &Store{client: client, cfg: cfg}, nil
}

// Put uploads bytes under key and returns the object's public URL.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	_, err := s.client.PutObject(ctx, s.cfg.Bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("put object %q: %w", key, err)
	}
	return fmt.Sprintf("%s/%s", s.cfg.PublicBaseURL, key), nil
}

func publicReadPolicy(bucket string) string {
	return fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": {"AWS": ["*"]},
			"Action": ["s3:GetObject"],
			"Resource": ["arn:aws:s3:::%s/*"]
		}]
	}`, bucket)
}
