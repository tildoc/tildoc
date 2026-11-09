// Package config loads tildoc configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings. All values come from TILDOC_* env vars.
type Config struct {
	DatabaseURL string
	DataDir     string
	BackupDir   string // server backups (default <DataDir>/backups)
	MasterKey   []byte // 32 bytes, encrypts access-key secrets

	HTTPAddr string // REST + WebDAV
	S3Addr   string
	SFTPAddr string

	S3Region       string
	PublicURL      string // e.g. https://tildoc.example:8443 (for connection help)
	S3PublicURL    string
	SFTPPublicAddr string

	CookieSecure   bool
	TrustedProxies []netip.Prefix

	TrashRetention   time.Duration
	MaxUploadBytes   int64
	ProvenanceWindow time.Duration
	MultipartMaxAge  time.Duration
	SessionTTL       time.Duration

	WorkerConcurrency int
	OCREnabled        bool
	OCRLangs          string
	PdftotextBin      string
	OcrmypdfBin       string
	GhostscriptBin    string
	OCRRedoForeign    bool // re-OCR PDFs that come with a foreign OCR layer (image-only variant + own OCR)
	ProcessTimeout    time.Duration

	// Office documents (Collabora Online via WOPI); OfficeURL empty = off.
	OfficeURL         string // public origin of Collabora, never tildoc's own
	OfficeInternalURL string // where tildoc fetches the discovery from
	WOPIURL           string // how Collabora reaches tildoc

	UIDir    string
	LogLevel slog.Level
}

// Load reads the configuration from the environment.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:    env("TILDOC_DATABASE_URL", ""),
		DataDir:        env("TILDOC_DATA_DIR", "./data"),
		HTTPAddr:       env("TILDOC_HTTP_ADDR", ":8080"),
		S3Addr:         env("TILDOC_S3_ADDR", ":9000"),
		SFTPAddr:       env("TILDOC_SFTP_ADDR", ":2222"),
		S3Region:       env("TILDOC_S3_REGION", "us-east-1"),
		PublicURL:      strings.TrimRight(env("TILDOC_PUBLIC_URL", "https://localhost:8443"), "/"),
		S3PublicURL:    strings.TrimRight(env("TILDOC_S3_PUBLIC_URL", "https://localhost:9443"), "/"),
		SFTPPublicAddr: env("TILDOC_SFTP_PUBLIC_ADDR", "localhost:2222"),
		OCRLangs:       env("TILDOC_OCR_LANGS", "deu+eng"),
		PdftotextBin:   env("TILDOC_PDFTOTEXT_BIN", "pdftotext"),
		OcrmypdfBin:    env("TILDOC_OCRMYPDF_BIN", "ocrmypdf"),
		GhostscriptBin: env("TILDOC_GHOSTSCRIPT_BIN", "gs"),
		UIDir:          env("TILDOC_UI_DIR", ""),
		OfficeURL:      strings.TrimRight(env("TILDOC_OFFICE_URL", ""), "/"),
		WOPIURL:        strings.TrimRight(env("TILDOC_WOPI_URL", ""), "/"),
	}
	c.OfficeInternalURL = strings.TrimRight(env("TILDOC_OFFICE_INTERNAL_URL", c.OfficeURL), "/")
	var errs []error
	c.CookieSecure = boolEnv("TILDOC_COOKIE_SECURE", true, &errs)
	c.OCREnabled = boolEnv("TILDOC_OCR_ENABLED", true, &errs)
	c.OCRRedoForeign = boolEnv("TILDOC_OCR_REDO_FOREIGN", true, &errs)
	c.TrashRetention = durEnv("TILDOC_TRASH_RETENTION", 30*24*time.Hour, &errs)
	c.ProvenanceWindow = durEnv("TILDOC_PROVENANCE_WINDOW", 2*time.Minute, &errs)
	c.MultipartMaxAge = durEnv("TILDOC_MULTIPART_MAX_AGE", 7*24*time.Hour, &errs)
	c.SessionTTL = durEnv("TILDOC_SESSION_TTL", 30*24*time.Hour, &errs)
	c.ProcessTimeout = durEnv("TILDOC_PROCESS_TIMEOUT", 30*time.Minute, &errs)
	c.MaxUploadBytes = intEnv("TILDOC_MAX_UPLOAD_BYTES", 50<<30, &errs)
	c.WorkerConcurrency = int(intEnv("TILDOC_WORKER_CONCURRENCY", 2, &errs))

	switch strings.ToLower(env("TILDOC_LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		c.LogLevel = slog.LevelInfo
	}

	for _, p := range splitList(env("TILDOC_TRUSTED_PROXIES", "")) {
		pfx, err := netip.ParsePrefix(p)
		if err != nil {
			addr, aerr := netip.ParseAddr(p)
			if aerr != nil {
				errs = append(errs, fmt.Errorf("TILDOC_TRUSTED_PROXIES: %q: %w", p, err))
				continue
			}
			pfx = netip.PrefixFrom(addr, addr.BitLen())
		}
		c.TrustedProxies = append(c.TrustedProxies, pfx)
	}

	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("TILDOC_DATABASE_URL is required"))
	}
	mk := env("TILDOC_MASTER_KEY", "")
	if mk == "" {
		errs = append(errs, errors.New("TILDOC_MASTER_KEY is required (32 bytes, base64; generate: openssl rand -base64 32)"))
	} else {
		key, err := base64.StdEncoding.DecodeString(mk)
		if err != nil || len(key) != 32 {
			errs = append(errs, errors.New("TILDOC_MASTER_KEY must be 32 bytes, base64 encoded"))
		}
		c.MasterKey = key
	}
	errs = append(errs, c.checkOffice()...)
	c.BackupDir = env("TILDOC_BACKUP_DIR", "")
	if c.BackupDir == "" {
		c.BackupDir = c.DataDir + "/backups"
	}
	if c.WorkerConcurrency < 1 {
		c.WorkerConcurrency = 1
	}
	return c, errors.Join(errs...)
}

// checkOffice validates the office settings. Collabora renders untrusted
// documents, so it must never share an origin with tildoc.
func (c *Config) checkOffice() (errs []error) {
	if c.OfficeURL == "" {
		return nil
	}
	origin := func(name, v string) string {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			errs = append(errs, fmt.Errorf("%s must be an http(s) origin without a path: %q", name, v))
			return ""
		}
		return strings.ToLower(u.Scheme + "://" + u.Host)
	}
	office := origin("TILDOC_OFFICE_URL", c.OfficeURL)
	origin("TILDOC_OFFICE_INTERNAL_URL", c.OfficeInternalURL)
	if c.WOPIURL == "" {
		errs = append(errs, errors.New("TILDOC_WOPI_URL is required with TILDOC_OFFICE_URL"))
	} else {
		origin("TILDOC_WOPI_URL", c.WOPIURL)
	}
	if office != "" && office == strings.ToLower(c.PublicURL) {
		errs = append(errs, errors.New("TILDOC_OFFICE_URL must be a different origin than TILDOC_PUBLIC_URL (use a subdomain)"))
	}
	return errs
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolEnv(k string, def bool, errs *[]error) bool {
	v := env(k, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", k, err))
		return def
	}
	return b
}

func durEnv(k string, def time.Duration, errs *[]error) time.Duration {
	v := env(k, "")
	if v == "" {
		return def
	}
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err == nil {
			return time.Duration(n) * 24 * time.Hour
		}
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", k, err))
		return def
	}
	return d
}

func intEnv(k string, def int64, errs *[]error) int64 {
	v := env(k, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", k, err))
		return def
	}
	return n
}
