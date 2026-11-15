// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package pki creates the certificate authority of a fleet and the
// certificates of its agents and its hub. The hub and the agents trust
// only this authority, so a machine without a certificate it signed can
// neither read an agent nor pass for one. Certificates are short-lived:
// replacing them often limits what a copied key is worth.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Names of the files of an authority.
const (
	CACertFile = "ca.crt"
	CAKeyFile  = "ca.key"
)

// Limits of the validity of the certificates.
const (
	DefaultCADays   = 5 * 365
	MaxCADays       = 20 * 365
	DefaultCertDays = 30
	MaxCertDays     = 366
	// backdate makes a certificate valid a little before it is issued,
	// for machines whose clock runs behind.
	backdate = 5 * time.Minute
)

// Roles of a certificate.
const (
	// RoleAgent certificates identify an agent to the hub that reads it.
	RoleAgent = "agent"
	// RoleHub certificates identify a hub to the agents it reads.
	RoleHub = "hub"
)

// Roles lists every role.
var Roles = []string{RoleAgent, RoleHub}

// organization marks the certificates of GreenLens; the role of a
// certificate is its organizational unit.
const organization = "GreenLens"

var certName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)

// ValidName reports whether a name can identify a node: lowercase letters,
// digits, dots and hyphens, as in a host name.
func ValidName(name string) bool { return certName.MatchString(name) }

// InitCA creates the key and the certificate of a new authority in dir.
// It refuses to replace an existing authority, whose certificates would
// stop being trusted.
func InitCA(dir, name string, days int, now time.Time) (*x509.Certificate, error) {
	if days < 1 || days > MaxCADays {
		return nil, fmt.Errorf("the validity must be between 1 and %d days", MaxCADays)
	}
	if name == "" {
		return nil, errors.New("the authority needs a name")
	}
	certPath, keyPath := filepath.Join(dir, CACertFile), filepath.Join(dir, CAKeyFile)
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Lstat(p); err == nil {
			return nil, fmt.Errorf("%s already exists; an authority is never replaced in place", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{organization}, CommonName: name},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// The authority signs certificates of nodes only, never another
		// authority.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeKey(keyPath, key); err != nil {
		return nil, err
	}
	if err := writeFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// Request describes a certificate to issue.
type Request struct {
	// Name identifies the node; it is the common name and a DNS name of
	// the certificate.
	Name string
	// Role is RoleAgent or RoleHub.
	Role string
	// Hosts are the other DNS names and the IP addresses the node is
	// reached at.
	Hosts []string
	// Days is how long the certificate is valid.
	Days int
}

// Issued is a new certificate with where it was written.
type Issued struct {
	Cert     *x509.Certificate
	CertPath string
	KeyPath  string
	// Replaced says whether a certificate of the same name was replaced.
	Replaced bool
}

// Issue creates a key and a certificate signed by the authority in caDir
// and writes them to outDir as <name>.crt and <name>.key. A certificate of
// the same name is replaced: that is how a certificate is renewed.
func Issue(caDir, outDir string, req Request, now time.Time) (Issued, error) {
	var out Issued
	if !ValidName(req.Name) {
		return out, errors.New("the name must be lowercase letters, digits, dots and hyphens, as a host name")
	}
	if req.Days < 1 || req.Days > MaxCertDays {
		return out, fmt.Errorf("the validity must be between 1 and %d days", MaxCertDays)
	}
	var usage x509.ExtKeyUsage
	switch req.Role {
	case RoleAgent:
		usage = x509.ExtKeyUsageServerAuth
	case RoleHub:
		usage = x509.ExtKeyUsageClientAuth
	default:
		return out, fmt.Errorf("the role must be %s or %s", RoleAgent, RoleHub)
	}
	ca, caKey, err := LoadCA(caDir)
	if err != nil {
		return out, err
	}
	notAfter := now.AddDate(0, 0, req.Days)
	if notAfter.After(ca.NotAfter) {
		return out, fmt.Errorf("the authority expires on %s, before the certificate would", ca.NotAfter.Format(time.DateOnly))
	}
	if now.After(ca.NotAfter) {
		return out, errors.New("the authority has expired")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return out, err
	}
	serial, err := newSerial()
	if err != nil {
		return out, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{organization}, OrganizationalUnit: []string{req.Role}, CommonName: req.Name},
		NotBefore:             now.Add(-backdate),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true,
		DNSNames:              []string{req.Name},
	}
	for _, h := range req.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		if !ValidName(h) {
			return out, fmt.Errorf("%q is neither an IP address nor a host name", h)
		}
		if h != req.Name {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return out, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return out, err
	}
	out.CertPath, out.KeyPath = filepath.Join(outDir, req.Name+".crt"), filepath.Join(outDir, req.Name+".key")
	if _, err := os.Lstat(out.CertPath); err == nil {
		out.Replaced = true
	}
	if err := writeKey(out.KeyPath, key); err != nil {
		return out, err
	}
	if err := writeFile(out.CertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return out, err
	}
	out.Cert, err = x509.ParseCertificate(der)
	return out, err
}

// LoadCA reads the certificate and the key of the authority in dir.
func LoadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := LoadCert(filepath.Join(dir, CACertFile))
	if err != nil {
		return nil, nil, err
	}
	if !cert.IsCA {
		return nil, nil, fmt.Errorf("%s is not the certificate of an authority", filepath.Join(dir, CACertFile))
	}
	keyPath := filepath.Join(dir, CAKeyFile)
	data, err := os.ReadFile(keyPath) //nolint:gosec // a path the operator names
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, nil, fmt.Errorf("%s holds no PEM key", keyPath)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, nil, fmt.Errorf("%s is not the key of %s", keyPath, filepath.Join(dir, CACertFile))
	}
	return cert, key, nil
}

// LoadCert reads the first certificate of a PEM file.
func LoadCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a path the operator names
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cert, nil
}

// Role returns the role of a certificate issued by Issue, or the empty
// string for any other certificate.
func Role(cert *x509.Certificate) string {
	if len(cert.Subject.Organization) != 1 || cert.Subject.Organization[0] != organization || len(cert.Subject.OrganizationalUnit) != 1 {
		return ""
	}
	switch r := cert.Subject.OrganizationalUnit[0]; r {
	case RoleAgent, RoleHub:
		return r
	}
	return ""
}

func newSerial() (*big.Int, error) {
	// 127 random bits: unique without a registry and positive, as the
	// standard asks.
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

// writeFile writes a file through a temporary one in the same directory,
// so a reader never sees half of it, and a key never exists with wider
// permissions than its own.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
