// Package media stores item pictures in an S3-compatible bucket.
package media

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders for uploads
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxSide is the longest edge a stored picture may have.
const MaxSide = 1600

// MaxPixels bounds the decoded size of an upload, so a small file cannot
// unpack into gigabytes of memory.
const MaxPixels = 50_000_000

var (
	// ErrNotImage is returned for an upload no decoder recognises.
	ErrNotImage = errors.New("not a supported image")
	// ErrTooLarge is returned for a picture with more than MaxPixels.
	ErrTooLarge = errors.New("image has too many pixels")
)

// Config is where the bucket is. It mirrors the keys of a Kitchen objectStore binding.
type Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
	CAFile          string
}

// ConfigFromEnv reads the S3_* variables. ok is false when no bucket is configured.
func ConfigFromEnv() (cfg Config, ok bool) {
	cfg = Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		Bucket:          os.Getenv("S3_BUCKET"),
		Region:          os.Getenv("S3_REGION"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		ForcePathStyle:  strings.EqualFold(os.Getenv("S3_FORCE_PATH_STYLE"), "true"),
		CAFile:          os.Getenv("S3_CA_FILE"),
	}
	return cfg, cfg.Endpoint != "" && cfg.Bucket != ""
}

// Store puts and gets pictures.
type Store struct {
	client *minio.Client
	bucket string
}

// New connects to the bucket described by cfg.
func New(cfg Config) (*Store, error) {
	host, secure := cfg.Endpoint, true
	if u, err := url.Parse(cfg.Endpoint); err == nil && u.Host != "" {
		host, secure = u.Host, u.Scheme != "http"
	}

	transport, err := minio.DefaultTransport(secure)
	if err != nil {
		return nil, err
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading the store's CA certificate: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("the store's CA certificate holds no certificate")
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.RootCAs = pool
	}

	lookup := minio.BucketLookupAuto
	if cfg.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
		Transport:    transport,
	})
	if err != nil {
		return nil, err
	}
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Put stores a JPEG under key.
func (s *Store) Put(ctx context.Context, key string, jpg []byte) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(jpg), int64(len(jpg)),
		minio.PutObjectOptions{ContentType: "image/jpeg", CacheControl: "private, max-age=31536000, immutable"})
	return err
}

// Get opens the object stored under key.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, err
	}
	return obj, info.Size, nil
}

// Delete removes the object stored under key. A missing object is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// IsNotFound reports whether err means the object does not exist.
func IsNotFound(err error) bool {
	var resp minio.ErrorResponse
	return errors.As(err, &resp) && (resp.StatusCode == http.StatusNotFound || resp.Code == "NoSuchKey")
}

// Normalize decodes an uploaded picture, scales it down to MaxSide and
// re-encodes it as JPEG. Re-encoding also drops any metadata the camera
// wrote, such as where the photo was taken.
func Normalize(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrNotImage
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return nil, ErrTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrNotImage
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, ErrNotImage
	}
	if w > MaxSide || h > MaxSide {
		if w >= h {
			h, w = h*MaxSide/w, MaxSide
		} else {
			w, h = w*MaxSide/h, MaxSide
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	// White behind transparent pixels, since JPEG has no alpha.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
