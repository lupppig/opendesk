// Package storage stores attachments and raw emails on local disk or any S3-compatible
// store (AWS S3, Cloudflare R2, MinIO).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// SignedURL returns a short-lived download URL, or "" when the driver cannot sign
	// (the API then streams the object itself).
	SignedURL(ctx context.Context, key, filename string, ttl time.Duration) (string, error)
}

var ErrNotFound = errors.New("storage: object not found")

type Options struct {
	Driver         string
	Dir            string
	Bucket         string
	Region         string
	Endpoint       string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
}

func New(ctx context.Context, o Options) (Storage, error) {
	switch o.Driver {
	case "", "local":
		if err := os.MkdirAll(o.Dir, 0o750); err != nil {
			return nil, err
		}
		return &local{dir: o.Dir}, nil
	case "s3":
		return newS3(ctx, o)
	}
	return nil, fmt.Errorf("unknown STORAGE_DRIVER %q", o.Driver)
}

// validKey refuses traversal; keys are generated server-side but this is the backstop.
func validKey(key string) error {
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return errors.New("storage: invalid key")
	}
	return nil
}

type local struct{ dir string }

func (l *local) path(key string) string { return filepath.Join(l.dir, filepath.FromSlash(key)) }

func (l *local) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if err := validKey(key); err != nil {
		return err
	}
	p := l.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (l *local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	f, err := os.Open(l.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l *local) Delete(_ context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	err := os.Remove(l.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l *local) SignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

type s3store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func newS3(ctx context.Context, o Options) (*s3store, error) {
	if o.Bucket == "" {
		return nil, errors.New("S3_BUCKET is required for STORAGE_DRIVER=s3")
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(o.Region)}
	if o.AccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(o.AccessKey, o.SecretKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(so *s3.Options) {
		if o.Endpoint != "" {
			so.BaseEndpoint = aws.String(o.Endpoint)
		}
		so.UsePathStyle = o.ForcePathStyle
	})
	return &s3store{client: client, presign: s3.NewPresignClient(client), bucket: o.Bucket}, nil
}

func (s *s3store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := validKey(key); err != nil {
		return err
	}
	// Stored files are people's: never kept by a browser or a shared cache.
	in := &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: r, ContentType: aws.String(contentType),
		CacheControl: aws.String("private, no-store")}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, in)
	return err
}

func (s *s3store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		if strings.Contains(err.Error(), "NoSuchKey") {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (s *s3store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}

func (s *s3store) SignedURL(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	disp := "attachment; filename*=UTF-8''" + url.PathEscape(filename)
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key,
		ResponseContentDisposition: &disp, ResponseCacheControl: aws.String("private, no-store")}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}
