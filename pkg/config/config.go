package config

import (
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config captures service level configuration loaded from config.yaml.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	CORS     CORSConfig     `yaml:"cors"`
	Upload   UploadConfig   `yaml:"upload"`
	Intranet IntranetConfig `yaml:"intranet"`
	Redis    RedisConfig    `yaml:"redis"`
	Storage  StorageConfig  `yaml:"storage"`
	Log      LogConfig      `yaml:"log"`
}

// LogConfig defines logging configuration.
type LogConfig struct {
	Level      string `yaml:"level"`       // debug, info, warn, error
	Output     string `yaml:"output"`      // stdout, file
	File       string `yaml:"file"`        // log file path
	MaxSize    int    `yaml:"max_size"`    // max file size in MB
	MaxBackups int    `yaml:"max_backups"` // max number of backup files
	MaxAge     int    `yaml:"max_age"`     // max age in days
	Compress   bool   `yaml:"compress"`    // compress old logs
	JSON       bool   `yaml:"json"`        // use JSON format
}

// StorageConfig defines storage backend configuration.
type StorageConfig struct {
	Type  string      `yaml:"type"` // local, minio
	Minio MinioConfig `yaml:"minio"`
}

// MinioConfig contains MinIO specific connection details.
type MinioConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	UseSSL    bool   `yaml:"use_ssl"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
}

// RedisConfig defines Redis connection settings for distributed locking.
type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Address  string `yaml:"address"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// AuthConfig defines admin login session settings.
type AuthConfig struct {
	CookieName           string   `yaml:"cookie_name"`
	CookieSecure         *bool    `yaml:"cookie_secure"`
	AllowInsecureLocal    bool     `yaml:"allow_insecure_localhost"`
	IdleTimeout          string   `yaml:"idle_timeout"`
	AbsoluteTimeout      string   `yaml:"absolute_timeout"`
	TrustedOrigins       []string `yaml:"trusted_origins"`
}

// CORSConfig defines CORS middleware settings.
type CORSConfig struct {
	AllowOrigin      string `yaml:"allow_origin" default:"*"`
	AllowMethods     string `yaml:"allow_methods" default:"*"`
	AllowHeaders     string `yaml:"allow_headers" default:"*"`
	AllowCredentials bool   `yaml:"allow_credentials" default:"false"`
}

// UploadConfig defines file upload constraints.
type UploadConfig struct {
	MaxSize      int64    `yaml:"max_size"`
	AllowedTypes []string `yaml:"allowed_types"`
}

// IntranetConfig defines intranet environment settings.
type IntranetConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ServerConfig defines HTTP server options.
type ServerConfig struct {
	Address  string `yaml:"address"`
	BasePath string `yaml:"base_path"`
}

// DatabaseConfig defines the database backend configuration.
type DatabaseConfig struct {
	Driver   string         `yaml:"driver"`
	SQLite   SQLiteConfig   `yaml:"sqlite"`
	MySQL    MySQLConfig    `yaml:"mysql"`
	Postgres PostgresConfig `yaml:"postgres"`
}

// SQLiteConfig contains SQLite specific settings.
type SQLiteConfig struct {
	Path string `yaml:"path"`
}

// MySQLConfig contains MySQL specific connection details.
type MySQLConfig struct {
	DSN string `yaml:"dsn"`
}

// PostgresConfig contains PostgreSQL specific connection details.
type PostgresConfig struct {
	DSN string `yaml:"dsn"`
}

// Load reads a YAML configuration file from the provided path.
// If the path is absolute, it reads directly from that path.
// If the path is relative, it searches in the current working directory first,
// then next to the binary executable.
func Load(name string) (*Config, error) {
	cfg := defaultConfig()

	var configPath string
	if filepath.IsAbs(name) {
		configPath = name
	} else {
		configPath = findConfigFile(name)
	}
	if configPath == "" {
		log.Printf("Warning: config file %q not found, using defaults", name)
		ApplyAuthDefaults(cfg)
		if _, err := ValidateAuthConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	log.Printf("Loading config from: %s", configPath)
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = f.Close() }()

	var parsed Config
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	applyDefaults(&parsed)
	ApplyAuthDefaults(&parsed)
	if _, err := ValidateAuthConfig(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Address: ":8080",
			// BasePath removed - now injected at build time
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{
				Path: "data/resource.db",
			},
		},
		CORS: CORSConfig{
			AllowOrigin:      "*",
			AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS",
			AllowHeaders:     "*",
			AllowCredentials: false,
		},
		Intranet: IntranetConfig{
			Enabled: false,
		},
		Upload: UploadConfig{
			MaxSize: 10 * 1024 * 1024, // 10MB
			AllowedTypes: []string{
				// 常见图片格式
				"image/jpeg",               // .jpg, .jpeg
				"image/png",                // .png
				"image/gif",                // .gif
				"image/webp",               // .webp
				"image/svg+xml",            // .svg
				"image/bmp",                // .bmp
				"image/x-ms-bmp",           // .bmp (Windows)
				"image/tiff",               // .tif, .tiff
				"image/x-icon",             // .ico
				"image/vnd.microsoft.icon", // .ico
				"image/heic",               // .heic (iOS)
				"image/heif",               // .heif
				// 压缩包格式
				"application/zip",              // .zip
				"application/x-zip-compressed", // .zip
				"application/x-rar-compressed", // .rar
				"application/x-7z-compressed",  // .7z
				"application/x-tar",            // .tar
				"application/gzip",             // .gz
				"application/x-gzip",           // .gz
				"application/x-bzip2",          // .bz2
				// 字体文件格式
				"font/ttf",               // .ttf
				"font/otf",               // .otf
				"font/woff",              // .woff
				"font/woff2",             // .woff2
				"application/x-font-ttf", // .ttf
				"application/x-font-otf", // .otf
				"application/font-woff",  // .woff
				"application/font-woff2", // .woff2
				// 文档格式
				"application/pdf",    // .pdf
				"application/msword", // .doc
				"application/vnd.openxmlformats-officedocument.wordprocessingml.document", // .docx
				"application/vnd.ms-excel", // .xls
				"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",         // .xlsx
				"application/vnd.ms-powerpoint",                                             // .ppt
				"application/vnd.openxmlformats-officedocument.presentationml.presentation", // .pptx
				// 文本格式
				"text/plain",       // .txt
				"text/csv",         // .csv
				"text/html",        // .html
				"text/css",         // .css
				"text/javascript",  // .js
				"application/json", // .json
				"application/xml",  // .xml
				"text/xml",         // .xml
				// 音视频格式
				"audio/mpeg",      // .mp3
				"audio/wav",       // .wav
				"audio/ogg",       // .ogg
				"video/mp4",       // .mp4
				"video/mpeg",      // .mpeg
				"video/webm",      // .webm
				"video/quicktime", // .mov
			},
		},
		Storage: StorageConfig{
			Type: "local",
			Minio: MinioConfig{
				Endpoint:  "localhost:9000",
				AccessKey: "minioadmin",
				SecretKey: "minioadmin123",
				UseSSL:    false,
				Bucket:    "rainbow-bridge",
				Region:    "us-east-1",
			},
		},
		Log: LogConfig{
			Level:      "info",
			Output:     "stdout",
			File:       "logs/rainbow-bridge.log",
			MaxSize:    100,
			MaxBackups: 10,
			MaxAge:     30,
			Compress:   true,
			JSON:       false,
		},
	}
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Address == "" {
		cfg.Server.Address = ":8080"
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.SQLite.Path == "" {
		cfg.Database.SQLite.Path = "data/resource.db"
	}
	if cfg.Storage.Type == "" {
		cfg.Storage.Type = "local"
	}
	if cfg.Storage.Minio.Endpoint == "" {
		cfg.Storage.Minio.Endpoint = "localhost:9000"
	}
	if cfg.Storage.Minio.AccessKey == "" {
		cfg.Storage.Minio.AccessKey = "minioadmin"
	}
	if cfg.Storage.Minio.SecretKey == "" {
		cfg.Storage.Minio.SecretKey = "minioadmin123"
	}
	if cfg.Storage.Minio.Bucket == "" {
		cfg.Storage.Minio.Bucket = "rainbow-bridge"
	}
	if cfg.Storage.Minio.Region == "" {
		cfg.Storage.Minio.Region = "us-east-1"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Output == "" {
		cfg.Log.Output = "stdout"
	}
	if cfg.Log.File == "" {
		cfg.Log.File = "logs/rainbow-bridge.log"
	}
	if cfg.Log.MaxSize <= 0 {
		cfg.Log.MaxSize = 100
	}
	if cfg.Log.MaxBackups <= 0 {
		cfg.Log.MaxBackups = 10
	}
	if cfg.Log.MaxAge <= 0 {
		cfg.Log.MaxAge = 30
	}
}

// findConfigFile searches for a config file in the current directory first,
// then next to the binary executable. Returns the full path or empty string.
func findConfigFile(name string) string {
	// 1. Current working directory
	if _, err := os.Stat(name); err == nil {
		abs, _ := filepath.Abs(name)
		return abs
	}

	// 2. Next to the binary executable
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		candidate := filepath.Join(exeDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

// NormalizeBasePath cleans up user input and returns a URL path prefix suitable for routing.
// Examples:
//
//	"", "/", " ."        -> ""
//	"rainbow"            -> "/rainbow"
//	"/rainbow/"          -> "/rainbow"
//	"/nested/prefix/"    -> "/nested/prefix"
func NormalizeBasePath(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(trimmed, "/"))
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return strings.TrimSuffix(cleaned, "/")
}
