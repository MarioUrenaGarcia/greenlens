// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// TokenPrefix starts every access token, so secret scanners and people
// recognize one.
const TokenPrefix = "greenlens_"

// The argon2id parameters are the first recommendation of OWASP: 19 MiB of
// memory, two passes and one lane. Verifications run one at a time and are
// cached, so the memory is needed briefly and rarely.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// cacheFor is how long a verified token is accepted without hashing it again.
const cacheFor = 5 * time.Minute

// reloadEvery bounds how often the file is checked for changes.
const reloadEvery = 2 * time.Second

var tokenName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ErrNotFound is returned when no token has the given id or name.
var ErrNotFound = errors.New("no such token")

// Token is a stored access token. The secret itself is never stored.
type Token struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Role      Role       `json:"role"`
	Hash      string     `json:"hash,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Expired reports whether t is past its expiry.
func (t Token) Expired(now time.Time) bool { return t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) }

type tokenFile struct {
	Version int     `json:"version"`
	Tokens  []Token `json:"tokens"`
}

type cached struct {
	id    string
	until time.Time
}

// Tokens is the token file of an agent.
type Tokens struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	list    []Token
	stamp   fileStamp
	checked time.Time
	cache   map[[sha256.Size]byte]cached

	// argon serializes verifications, so a flood of bad tokens cannot
	// make the agent allocate memory for many hashes at once.
	argon sync.Mutex
}

type fileStamp struct {
	mod  time.Time
	size int64
}

// OpenTokens reads the token file at path. A missing file is an empty list.
// A file that others than its owner can read is refused.
func OpenTokens(path string) (*Tokens, error) {
	t := &Tokens{path: path, now: time.Now, cache: map[[sha256.Size]byte]cached{}}
	if err := t.load(); err != nil {
		return nil, err
	}
	return t, nil
}

// Path returns the token file.
func (t *Tokens) Path() string { return t.path }

func (t *Tokens) load() error {
	st, err := os.Stat(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		t.list, t.stamp = nil, fileStamp{}
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s can be read by others; it must be readable only by its owner (chmod 600)", t.path)
	}
	data, err := os.ReadFile(t.path)
	if err != nil {
		return err
	}
	var f tokenFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("%s: %w", t.path, err)
	}
	if f.Version != 1 {
		return fmt.Errorf("%s: unsupported version %d", t.path, f.Version)
	}
	for _, tk := range f.Tokens {
		if tk.ID == "" || !tokenName.MatchString(tk.Name) || !tk.Role.Valid() || !strings.HasPrefix(tk.Hash, "$argon2id$") {
			return fmt.Errorf("%s: malformed token %q", t.path, tk.Name)
		}
	}
	t.list, t.stamp = f.Tokens, fileStamp{st.ModTime(), st.Size()}
	clear(t.cache)
	return nil
}

// refresh rereads the file when another process changed it. It is called
// with t.mu held.
func (t *Tokens) refresh() {
	now := t.now()
	if now.Sub(t.checked) < reloadEvery {
		return
	}
	t.checked = now
	st, err := os.Stat(t.path)
	var stamp fileStamp
	if err == nil {
		stamp = fileStamp{st.ModTime(), st.Size()}
	}
	if stamp != t.stamp {
		if err := t.load(); err != nil {
			// The previous list stays in force; the next check tries again.
			t.stamp = fileStamp{}
		}
	}
}

func (t *Tokens) save() error {
	data, err := json.MarshalIndent(tokenFile{Version: 1, Tokens: t.list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(t.path), ".tokens-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, t.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(t.path); err == nil {
		t.stamp = fileStamp{st.ModTime(), st.Size()}
	}
	return nil
}

// Len returns the number of tokens that have not expired.
func (t *Tokens) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	n := 0
	for _, tk := range t.list {
		if !tk.Expired(t.now()) {
			n++
		}
	}
	return n
}

// List returns the tokens without their hashes, oldest first.
func (t *Tokens) List() []Token {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	out := make([]Token, 0, len(t.list))
	for _, tk := range t.list {
		tk.Hash = ""
		out = append(out, tk)
	}
	return out
}

// Create adds a token and returns its secret, which is shown once and
// cannot be recovered. ttl zero means it never expires.
func (t *Tokens) Create(name string, role Role, ttl time.Duration) (string, Token, error) {
	if !tokenName.MatchString(name) {
		return "", Token{}, errors.New("the name must be 1 to 63 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if !role.Valid() {
		return "", Token{}, fmt.Errorf("unknown role %q", role)
	}
	if ttl < 0 {
		return "", Token{}, errors.New("the expiry must be positive")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checked = time.Time{}
	t.refresh()
	for _, tk := range t.list {
		if tk.Name == name {
			return "", Token{}, fmt.Errorf("a token named %q already exists", name)
		}
	}
	idBytes := make([]byte, 6)
	if _, err := rand.Read(idBytes); err != nil {
		return "", Token{}, err
	}
	id := hex.EncodeToString(idBytes)
	secret, err := randomText(32)
	if err != nil {
		return "", Token{}, err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", Token{}, err
	}
	now := t.now().UTC().Truncate(time.Second)
	tk := Token{ID: id, Name: name, Role: role, Hash: hashSecret(secret, salt), CreatedAt: now}
	if ttl > 0 {
		exp := now.Add(ttl)
		tk.ExpiresAt = &exp
	}
	t.list = append(t.list, tk)
	if err := t.save(); err != nil {
		t.list = t.list[:len(t.list)-1]
		return "", Token{}, err
	}
	tk.Hash = ""
	return TokenPrefix + id + "_" + secret, tk, nil
}

// Revoke removes the token with the given id or name.
func (t *Tokens) Revoke(idOrName string) (Token, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checked = time.Time{}
	t.refresh()
	i := slices.IndexFunc(t.list, func(tk Token) bool { return tk.ID == idOrName || tk.Name == idOrName })
	if i < 0 {
		return Token{}, ErrNotFound
	}
	tk := t.list[i]
	prev := t.list
	t.list = slices.Delete(slices.Clone(t.list), i, i+1)
	if err := t.save(); err != nil {
		t.list = prev
		return Token{}, err
	}
	for k, c := range t.cache {
		if c.id == tk.ID {
			delete(t.cache, k)
		}
	}
	tk.Hash = ""
	return tk, nil
}

// Verify returns the token a secret belongs to. Unknown, malformed,
// revoked and expired secrets are all rejected the same way.
func (t *Tokens) Verify(secret string) (Token, bool) {
	id, raw, ok := splitSecret(secret)
	if !ok {
		return Token{}, false
	}
	key := sha256.Sum256([]byte(secret))
	t.mu.Lock()
	t.refresh()
	now := t.now()
	i := slices.IndexFunc(t.list, func(tk Token) bool { return tk.ID == id })
	if i < 0 || t.list[i].Expired(now) {
		t.mu.Unlock()
		return Token{}, false
	}
	tk := t.list[i]
	if c, ok := t.cache[key]; ok && c.id == id && now.Before(c.until) {
		t.mu.Unlock()
		tk.Hash = ""
		return tk, true
	}
	t.mu.Unlock()

	t.argon.Lock()
	good := checkSecret(raw, tk.Hash)
	t.argon.Unlock()
	if !good {
		return Token{}, false
	}
	t.mu.Lock()
	t.cache[key] = cached{id: id, until: now.Add(cacheFor)}
	t.mu.Unlock()
	tk.Hash = ""
	return tk, true
}

func splitSecret(s string) (id, raw string, ok bool) {
	rest, ok := strings.CutPrefix(s, TokenPrefix)
	if !ok {
		return "", "", false
	}
	id, raw, ok = strings.Cut(rest, "_")
	if !ok || len(id) != 12 || len(raw) != 43 {
		return "", "", false
	}
	return id, raw, true
}

func hashSecret(secret string, salt []byte) string {
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// checkSecret compares a secret with a stored hash in constant time, with
// the parameters recorded in the hash.
func checkSecret(secret, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var m, tm uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &tm, &p); err != nil || m == 0 || m > 1<<20 || tm == 0 || tm > 10 || p == 0 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, tm, m, p, uint32(len(want))) //nolint:gosec // len(want) is at most 64
	return subtle.ConstantTimeCompare(got, want) == 1
}
