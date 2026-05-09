package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"

	"github.com/xianxu/nous/lib/provider/vault/keychain"
)

const (
	caCertAccount = "_ca:cert"
	caKeyAccount  = "_ca:key"
)

// CA holds a certificate authority for TLS interception.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte // PEM-encoded certificate
}

// LoadOrCreateCA loads the CA from macOS Keychain, or creates and stores a new one.
//
// CA storage shares the same keychain service-name namespace as
// credentials (`charon` for signed installs, `charon-dev` for unsigned
// dev binaries). This means a dev binary can't read the signed binary's
// CA — which is the right thing: dev runs that need a CA either
// regenerate one or hit Allow/Deny if they try to share state.
func LoadOrCreateCA() (*CA, error) {
	caService := keychain.ResolveServiceName()

	// Try loading existing CA from keychain.
	certPEM, certErr := keychain.GetRaw(caService, caCertAccount)
	keyPEM, keyErr := keychain.GetRaw(caService, caKeyAccount)
	if certErr == nil && keyErr == nil {
		ca, err := parseCA([]byte(certPEM), []byte(keyPEM))
		if err == nil && time.Now().Before(ca.Cert.NotAfter.Add(-24*time.Hour)) {
			return ca, nil
		}
		// Expired or corrupt — regenerate.
	}

	// Generate new CA.
	ca, err := generateCA()
	if err != nil {
		return nil, err
	}

	// Store cert in keychain.
	if err := keychain.SetRaw(caService, caCertAccount, string(ca.CertPEM)); err != nil {
		return nil, err
	}

	// Store key in keychain.
	keyDER, err := x509.MarshalECPrivateKey(ca.Key)
	if err != nil {
		return nil, err
	}
	keyPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := keychain.SetRaw(caService, caKeyAccount, string(keyPEMBytes)); err != nil {
		return nil, err
	}

	return ca, nil
}

// NewTestCA creates a new CA without keychain storage. For testing only.
func NewTestCA() (*CA, error) {
	return generateCA()
}

func generateCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject: pkix.Name{
			CommonName:   "Charon Proxy CA",
			Organization: []string{"Charon"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	return &CA{Cert: cert, Key: key, CertPEM: certPEM}, nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}

	return &CA{Cert: cert, Key: key, CertPEM: certPEM}, nil
}

// GenerateCert creates a TLS certificate for the given hostname, signed by this CA.
func (ca *CA) GenerateCert(hostname string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject: pkix.Name{
			CommonName: hostname,
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	if ip := net.ParseIP(hostname); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{hostname}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

func marshalKeyPEM(key *ecdsa.PrivateKey) []byte {
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func randomSerial() *big.Int {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}
