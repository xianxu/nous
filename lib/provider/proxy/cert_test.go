package proxy

import (
	"crypto/x509"
	"testing"
)

func TestGenerateCA(t *testing.T) {
	ca, err := generateCA()
	if err != nil {
		t.Fatal(err)
	}
	if !ca.Cert.IsCA {
		t.Error("expected CA cert to have IsCA=true")
	}
	if ca.Cert.Subject.CommonName != "Charon Proxy CA" {
		t.Errorf("unexpected CN: %s", ca.Cert.Subject.CommonName)
	}
	if len(ca.CertPEM) == 0 {
		t.Error("CertPEM is empty")
	}
}

func TestGenerateCA_ParseRoundTrip(t *testing.T) {
	ca, err := generateCA()
	if err != nil {
		t.Fatal(err)
	}

	// Serialize key to PEM, then parse back.
	keyPEM := marshalKeyPEM(ca.Key)
	ca2, err := parseCA(ca.CertPEM, keyPEM)
	if err != nil {
		t.Fatalf("parseCA round-trip failed: %v", err)
	}
	if string(ca.CertPEM) != string(ca2.CertPEM) {
		t.Error("cert PEM mismatch after round-trip")
	}
}

func TestGenerateCert_DNSName(t *testing.T) {
	ca, err := generateCA()
	if err != nil {
		t.Fatal(err)
	}

	cert, err := ca.GenerateCert("example.com")
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "example.com" {
		t.Errorf("expected DNSNames=[example.com], got %v", leaf.DNSNames)
	}
	if len(leaf.IPAddresses) != 0 {
		t.Errorf("expected no IP SANs for hostname, got %v", leaf.IPAddresses)
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Errorf("cert not valid under CA: %v", err)
	}
}

func TestGenerateCert_IPAddress(t *testing.T) {
	ca, err := generateCA()
	if err != nil {
		t.Fatal(err)
	}

	cert, err := ca.GenerateCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "127.0.0.1" {
		t.Errorf("expected IPAddresses=[127.0.0.1], got %v", leaf.IPAddresses)
	}
	if len(leaf.DNSNames) != 0 {
		t.Errorf("expected no DNS SANs for IP, got %v", leaf.DNSNames)
	}
}

func TestGenerateCert_UniqueSerials(t *testing.T) {
	ca, err := generateCA()
	if err != nil {
		t.Fatal(err)
	}

	serials := make(map[string]bool)
	for i := 0; i < 100; i++ {
		cert, err := ca.GenerateCert("example.com")
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := x509.ParseCertificate(cert.Certificate[0])
		s := leaf.SerialNumber.String()
		if serials[s] {
			t.Fatalf("duplicate serial number on iteration %d", i)
		}
		serials[s] = true
	}
}
