// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package proxy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// SaltFile is the name of the file, in the data directory, that holds the
// salt of the consumer HMACs.
const SaltFile = "consumer.salt"

const saltSize = 32

// LoadSalt reads the salt in path, creating it with fresh random bytes,
// readable only by its owner, when it does not exist.
func LoadSalt(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is in the data directory of the agent
	if err == nil {
		if len(b) != saltSize {
			return nil, fmt.Errorf("%s: a salt has %d bytes, found %d", path, saltSize, len(b))
		}
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, saltSize)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the path is in the data directory of the agent
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return nil, err
	}
	return b, f.Close()
}

var digest = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)

// LoadAliases reads a YAML map of consumer names to the SHA-256 of their
// header value, written as "sha256:<hex>". The file holds digests, never
// keys.
func LoadAliases(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path comes from the configuration
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]string{}
	for name, d := range m {
		sub := digest.FindStringSubmatch(strings.ToLower(strings.TrimSpace(d)))
		if sub == nil {
			return nil, fmt.Errorf("%s: %s: expected sha256:<64 hexadecimal digits>", path, name)
		}
		if !consumerName.MatchString(name) {
			return nil, fmt.Errorf("%s: %q: names are 1 to 63 letters, digits, '.', '_' or '-'", path, name)
		}
		out[sub[1]] = name
	}
	return out, nil
}

var consumerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// Consumers names the consumer of each request from a header. The value
// of the header is never kept: it is looked up by its SHA-256 in the
// aliases, and otherwise named after its HMAC with the local salt. It is
// safe for concurrent use.
type Consumers struct {
	header  string
	salt    []byte
	mu      sync.Mutex
	aliases map[string]string
	// register records a new consumer, with its HMAC when it has no alias.
	register func(name, keyHMAC string)
	known    map[string]bool
}

// NewConsumers identifies consumers by header.
func NewConsumers(header string, salt []byte, aliases map[string]string, register func(name, keyHMAC string)) *Consumers {
	return &Consumers{header: header, salt: salt, aliases: aliases, register: register, known: map[string]bool{}}
}

// SetAliases replaces the aliases, after the file was read again.
func (c *Consumers) SetAliases(a map[string]string) {
	c.mu.Lock()
	c.aliases = a
	c.mu.Unlock()
}

// Name returns the consumer of a request with the header value v, or ""
// without one.
func (c *Consumers) Name(v string) string {
	if c == nil || v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(v))
	c.mu.Lock()
	alias, ok := c.aliases[hex.EncodeToString(sum[:])]
	c.mu.Unlock()
	keyHMAC := ""
	if !ok {
		mac := hmac.New(sha256.New, c.salt)
		mac.Write([]byte(v))
		keyHMAC = hex.EncodeToString(mac.Sum(nil))
		alias = "consumer-" + keyHMAC[:12]
	}
	c.mu.Lock()
	first := !c.known[alias]
	c.known[alias] = true
	c.mu.Unlock()
	if first && c.register != nil {
		c.register(alias, keyHMAC)
	}
	return alias
}
