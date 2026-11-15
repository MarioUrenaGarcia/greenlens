// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package pki

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// ReloadEvery bounds how often the files of a KeyPair are looked at.
const ReloadEvery = 10 * time.Second

// KeyPair holds a certificate and its key and loads them again when their
// files change, so a renewed certificate takes effect without a restart.
type KeyPair struct {
	certFile, keyFile string
	failed            func(error)
	now               func() time.Time

	mu      sync.Mutex
	cert    *tls.Certificate
	modTime [2]time.Time
	checked time.Time
}

// NewKeyPair loads the pair. failed is told when a later reload fails;
// the previous pair then stays in use.
func NewKeyPair(certFile, keyFile string, failed func(error)) (*KeyPair, error) {
	k := &KeyPair{certFile: certFile, keyFile: keyFile, failed: failed, now: time.Now}
	if err := k.load(); err != nil {
		return nil, err
	}
	k.checked = k.now()
	return k, nil
}

func (k *KeyPair) times() ([2]time.Time, error) {
	var out [2]time.Time
	for i, f := range []string{k.certFile, k.keyFile} {
		st, err := os.Stat(f)
		if err != nil {
			return out, err
		}
		out[i] = st.ModTime()
	}
	return out, nil
}

func (k *KeyPair) load() error {
	times, err := k.times()
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(k.certFile, k.keyFile)
	if err != nil {
		return err
	}
	k.cert, k.modTime = &cert, times
	return nil
}

// current returns the pair, after loading it again when its files
// changed since they were last looked at.
func (k *KeyPair) current() *tls.Certificate {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now := k.now(); now.Sub(k.checked) >= ReloadEvery {
		k.checked = now
		if times, err := k.times(); err != nil {
			k.failed(err)
		} else if times != k.modTime {
			// The certificate and its key are replaced one after the
			// other; a pair that does not match yet is tried again.
			if err := k.load(); err != nil {
				k.failed(err)
			}
		}
	}
	return k.cert
}

// Server implements tls.Config.GetCertificate.
func (k *KeyPair) Server(*tls.ClientHelloInfo) (*tls.Certificate, error) { return k.current(), nil }

// Client implements tls.Config.GetClientCertificate.
func (k *KeyPair) Client(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return k.current(), nil
}
