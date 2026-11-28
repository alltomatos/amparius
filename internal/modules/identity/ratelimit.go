package identity

import (
	"sync"
	"time"
)

// rateLimiter é uma defesa adicional em memória por IP+e-mail (ADR 0048 §6,
// contrato auth-login-http-v1). O contador autoritativo contra força bruta
// continua sendo failed_attempts/locked_until em identity.app_user, que
// nunca falha aberto; este limitador é um desvio documentado do Redis citado
// no contrato (go.mod não tem cliente Redis nesta fatia), não persiste entre
// reinícios e não é compartilhado entre réplicas do processo.
type rateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	attempts map[string][]time.Time
}

func newRateLimiter(window time.Duration, max int) *rateLimiter {
	return &rateLimiter{window: window, max: max, attempts: make(map[string][]time.Time)}
}

// Allow registra uma tentativa para key e devolve false quando a janela
// deslizante já atingiu o limite.
func (l *rateLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.attempts[key][:0]
	for _, at := range l.attempts[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.max {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}

// idempotencyCache guarda a última resposta de POST /v1/auth/refresh por
// Idempotency-Key, associada ao refresh token recebido, para devolver a
// mesma sessão sem rotacionar de novo em reenvios de rede (contrato
// auth-login-http-v1 "Idempotência do refresh"). Em memória por processo,
// mesmo desvio de Redis assumido para o rateLimiter.
type idempotencyCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]idempotencyEntry
}

type idempotencyEntry struct {
	refreshTokenHash string
	response         SessionResult
	expiresAt        time.Time
}

func newIdempotencyCache(ttl time.Duration) *idempotencyCache {
	return &idempotencyCache{ttl: ttl, items: make(map[string]idempotencyEntry)}
}

// Get devolve a resposta em cache quando key existe, não expirou e foi
// gravada para o mesmo refresh token (nunca devolve a resposta de um
// Idempotency-Key reaproveitado com um token diferente).
func (c *idempotencyCache) Get(key, refreshTokenHash string) (SessionResult, bool) {
	if c == nil || key == "" {
		return SessionResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok || entry.expiresAt.Before(time.Now()) || entry.refreshTokenHash != refreshTokenHash {
		return SessionResult{}, false
	}
	return entry.response, true
}

func (c *idempotencyCache) Put(key, refreshTokenHash string, response SessionResult) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.items[key] = idempotencyEntry{refreshTokenHash: refreshTokenHash, response: response, expiresAt: now.Add(c.ttl)}
	for k, v := range c.items {
		if v.expiresAt.Before(now) {
			delete(c.items, k)
		}
	}
}
