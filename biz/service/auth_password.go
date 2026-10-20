package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// 密码编码:Argon2id + 随机盐,PHC 字符串保存算法、参数、盐与摘要。
// 参数范围收紧,避免损坏或被篡改的记录触发无界计算。
const (
	authPasswordMinChars  = 15
	authPasswordMaxChars  = 128
	authPasswordMaxBytes  = 512
	authUsernameMinChars  = 3
	authUsernameMaxChars  = 32
	authSaltBytes         = 16
	authHashBytes         = 32
	authArgonMemoryKiB    = 19456 // OWASP: 19 MiB
	authArgonIterations   = 2
	authArgonParallelism  = 1
	authArgonMemoryMaxKiB = 65536
	authArgonIterMax      = 4
	authArgonParallelMax  = 4
)

var (
	errAuthUsernameInvalid = errors.New("auth username must match [a-zA-Z0-9_-]{3,32}")
	errAuthPasswordInvalid = errors.New("auth password must be 15-128 characters and at most 512 bytes")
)

// IsValidAuthUsername 与初始化、登录共用同一规则;大小写敏感,不做隐式转换。
func IsValidAuthUsername(username string) bool {
	if len(username) < authUsernameMinChars || len(username) > authUsernameMaxChars {
		return false
	}
	for _, r := range username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// IsValidAuthPassword 允许空格与中文,不 trim、不截断,不要求字符类别组合。
func IsValidAuthPassword(password string) bool {
	count := utf8.RuneCountInString(password)
	if count < authPasswordMinChars || count > authPasswordMaxChars {
		return false
	}
	return len(password) <= authPasswordMaxBytes
}

// hashAdminPassword 生成 PHC 字符串;失败时不落库任何内容。
func hashAdminPassword(password string) (string, error) {
	salt := make([]byte, authSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(password), salt, authArgonIterations, authArgonMemoryKiB, authArgonParallelism, authHashBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, authArgonMemoryKiB, authArgonIterations, authArgonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// argon2Params 是从 PHC 字符串解析出的参数。
type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	digest      []byte
}

// parseArgon2Hash 解析并校验 PHC 参数范围;超范围的记录按不可用处理。
func parseArgon2Hash(encoded string) (*argon2Params, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, errors.New("unsupported password hash format")
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return nil, errors.New("unsupported argon2 version")
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return nil, errors.New("malformed argon2 parameters")
	}
	if memory == 0 || iterations == 0 || parallelism == 0 ||
		memory > authArgonMemoryMaxKiB || iterations > authArgonIterMax || parallelism > authArgonParallelMax {
		return nil, errors.New("argon2 parameters outside accepted range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < authSaltBytes {
		return nil, errors.New("malformed argon2 salt")
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(digest) < authHashBytes {
		return nil, errors.New("malformed argon2 digest")
	}
	return &argon2Params{memory: memory, iterations: iterations, parallelism: parallelism, salt: salt, digest: digest}, nil
}

// verifyAdminPassword 常量时间比较;哈希不可解析时返回错误(由调用方按凭据错误处理)。
func verifyAdminPassword(encoded, password string) (bool, error) {
	params, err := parseArgon2Hash(encoded)
	if err != nil {
		return false, err
	}
	computed := argon2.IDKey([]byte(password), params.salt, params.iterations, params.memory, params.parallelism, uint32(len(params.digest)))
	return subtle.ConstantTimeCompare(computed, params.digest) == 1, nil
}

// authVirtualHash 是未知账号时用于固定开销校验的假哈希:避免明显的快速返回差异。
// 首次使用时生成一次,参数与真实哈希一致。
var authVirtualHashOnce = func() func() string {
	cached := ""
	return func() string {
		if cached == "" {
			hash, err := hashAdminPassword("rainbow-bridge-virtual-password-placeholder")
			if err != nil {
				return ""
			}
			cached = hash
		}
		return cached
	}
}()