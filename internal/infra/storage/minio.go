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
	"net/url"
	"time"

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
	// presignClient signs PresignPut URLs — see PresignPut's doc for why
	// this must be a separate client from the one above whenever cfg.Endpoint
	// (used for every other, server-to-server call) isn't itself
	// browser-reachable, which is exactly the docker-compose case
	// ("minio:9000") that same doc flags.
	presignClient *minio.Client
	cfg           Config
}

// awsRegion is fixed rather than autodetected: minio-go otherwise discovers
// a bucket's region lazily via a live GetBucketLocation call the first time
// it's needed for SigV4 signing, and PresignPut must never depend on that —
// it's meant to be pure local HMAC computation, no network round trip, and
// especially not one to presignClient's (browser-facing, possibly currently
// unreachable from the server itself) host. This MinIO deployment is never
// given MINIO_REGION/MINIO_SITE_REGION (checked: neither docker-compose.yml
// nor .env sets either), so it runs on MinIO's own default, "us-east-1".
const awsRegion = "us-east-1"

func New(ctx context.Context, cfg Config) (*Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: awsRegion,
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

	presignClient := client
	if presignEndpoint, presignSSL, ok := parsePublicEndpoint(cfg.PublicBaseURL); ok && presignEndpoint != cfg.Endpoint {
		pc, err := minio.New(presignEndpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
			Secure: presignSSL,
			Region: awsRegion,
		})
		if err != nil {
			return nil, fmt.Errorf("construct presign-only minio client for %q: %w", presignEndpoint, err)
		}
		presignClient = pc
	}

	return &Store{client: client, presignClient: presignClient, cfg: cfg}, nil
}

// parsePublicEndpoint pulls the host (and scheme) a browser can actually
// reach out of PublicBaseURL, e.g. "https://example.trycloudflare.com/aigc-assets"
// -> ("example.trycloudflare.com", true, true). ok is false for anything
// that doesn't parse as an absolute http(s) URL, so New falls back to
// signing with cfg.Endpoint (today's behavior) rather than silently using a
// broken host.
func parsePublicEndpoint(publicBaseURL string) (host string, useSSL bool, ok bool) {
	u, err := url.Parse(publicBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, false
	}
	return u.Host, u.Scheme == "https", true
}

// Put uploads bytes under key and returns the object's public URL.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	_, err := s.client.PutObject(ctx, s.cfg.Bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("put object %q: %w", key, err)
	}
	return fmt.Sprintf("%s/%s", s.cfg.PublicBaseURL, key), nil
}

// PublicURLFor returns what an object's public_url will be once uploaded
// under key, without touching the network — the presigned-upload flow
// (F2.1) needs this predictable up front, since the browser uploads
// directly and only reports success back. Kept in exact lockstep with
// Put()'s own formula so the two can never drift.
func (s *Store) PublicURLFor(key string) string {
	return fmt.Sprintf("%s/%s", s.cfg.PublicBaseURL, key)
}

// ObjectInfo is Stat's result: what the object store itself recorded for an
// uploaded object, trusted over anything a client claims about the same
// file (see handleCompleteAsset's doc for why mime/size specifically are
// taken from here, not the request body).
type ObjectInfo struct {
	Mime      string
	SizeBytes int64
}

// Stat confirms an object exists under key — the presigned-upload
// completion step (F2.1) uses this to verify the browser's PUT actually
// landed before a citable assets row is created for it, rather than trusting
// a "complete" call that never checks anything really happened.
func (s *Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.cfg.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat object %q: %w", key, err)
	}
	return ObjectInfo{Mime: info.ContentType, SizeBytes: info.Size}, nil
}

// PresignPut returns a time-limited URL the browser can PUT bytes to
// directly (F2.1: "预签名直传，不经过 Go 服务"). The SigV4 signature binds to
// whatever host it was signed for, so this deliberately uses presignClient
// (host derived from cfg.PublicBaseURL — the same address browsers already
// use for public_url downloads) rather than the client's own cfg.Endpoint.
// In docker-compose, Endpoint is the container-internal "minio:9000",
// unreachable from outside the docker network; New falls back to signing
// with Endpoint only if PublicBaseURL doesn't parse to a usable host.
func (s *Store) PresignPut(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := s.presignClient.PresignedPutObject(ctx, s.cfg.Bucket, key, expiry)
	if err != nil {
		return "", fmt.Errorf("presign put %q: %w", key, err)
	}
	return u.String(), nil
}

// Delete removes an object outright — the recycle-bin hard-purge duty
// (upkeep.Runner's autoPurgeTrash) is this method's only caller: a
// soft-deleted asset's row/storage both need to actually go away once its
// 30-day grace period (§07's own ask) is up, not just sit as an orphaned
// blob nobody's public_url points to anymore. MinIO returns success for a
// key that's already gone, so this is safe to retry.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete object %q: %w", key, err)
	}
	return nil
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
