package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// PasswordAlgoArgon2idV1 é o rótulo de versão gravado em password_algo,
// permitindo re-hash em etapas quando os parâmetros mudarem (ADR 0048 §2).
const PasswordAlgoArgon2idV1 = "argon2id:v1"

// ErrInvalidPasswordHash indica um hash armazenado fora do formato esperado.
var ErrInvalidPasswordHash = errors.New("hash de senha inválido")

type argon2Params struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

var currentArgon2Params = argon2Params{memoryKiB: 19 * 1024, iterations: 2, parallelism: 1}

const (
	argon2SaltLen = 16
	argon2KeyLen  = 32
)

// HashPassword gera um hash argon2id autodescritivo (salt e parâmetros
// embutidos) e devolve a versão lógica atual para password_algo.
func HashPassword(password string) (hash string, algo string, err error) {
	if password == "" {
		return "", "", errors.New("senha vazia")
	}
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", "", fmt.Errorf("gerar salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, currentArgon2Params.iterations, currentArgon2Params.memoryKiB, currentArgon2Params.parallelism, argon2KeyLen)
	return encodeArgon2Hash(salt, digest), PasswordAlgoArgon2idV1, nil
}

// VerifyPassword confere a senha contra o hash autodescritivo. needsRehash
// sinaliza password_algo desatualizado ou parâmetros divergentes dos atuais,
// para re-hash oportunista no login bem-sucedido.
func VerifyPassword(hash, algo, password string) (ok bool, needsRehash bool, err error) {
	salt, digest, params, err := decodeArgon2Hash(hash)
	if err != nil {
		return false, false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, params.iterations, params.memoryKiB, params.parallelism, uint32(len(digest)))
	match := subtle.ConstantTimeCompare(candidate, digest) == 1
	outdated := algo != PasswordAlgoArgon2idV1 || params != currentArgon2Params
	return match, match && outdated, nil
}

var decoyOnce sync.Once
var decoyHashValue string

// DecoyHash devolve um hash argon2id fixo, que nunca corresponde a uma senha
// real, usado para igualar o custo de CPU quando o e-mail é desconhecido
// (defesa contra enumeração por tempo de resposta, ADR 0048 §5).
func DecoyHash() string {
	decoyOnce.Do(func() {
		hash, _, err := HashPassword("amparius-decoy-" + strings.Repeat("x", 32))
		if err != nil {
			panic("gerar hash de isca: " + err.Error())
		}
		decoyHashValue = hash
	})
	return decoyHashValue
}

func encodeArgon2Hash(salt, digest []byte) string {
	p := currentArgon2Params
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.memoryKiB, p.iterations, p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest))
}

func decodeArgon2Hash(encoded string) ([]byte, []byte, argon2Params, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || !strings.HasPrefix(parts[2], "v=") {
		return nil, nil, argon2Params{}, ErrInvalidPasswordHash
	}
	params, err := parseArgon2Params(parts[3])
	if err != nil {
		return nil, nil, argon2Params{}, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, argon2Params{}, ErrInvalidPasswordHash
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, argon2Params{}, ErrInvalidPasswordHash
	}
	return salt, digest, params, nil
}

func parseArgon2Params(segment string) (argon2Params, error) {
	fields := strings.Split(segment, ",")
	if len(fields) != 3 {
		return argon2Params{}, ErrInvalidPasswordHash
	}
	var params argon2Params
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return argon2Params{}, ErrInvalidPasswordHash
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return argon2Params{}, ErrInvalidPasswordHash
		}
		switch key {
		case "m":
			params.memoryKiB = uint32(parsed)
		case "t":
			params.iterations = uint32(parsed)
		case "p":
			params.parallelism = uint8(parsed)
		default:
			return argon2Params{}, ErrInvalidPasswordHash
		}
	}
	if params.memoryKiB == 0 || params.iterations == 0 || params.parallelism == 0 {
		return argon2Params{}, ErrInvalidPasswordHash
	}
	return params, nil
}
