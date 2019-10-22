package util

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"io/ioutil"
)

type CA struct {
	Key  *rsa.PrivateKey
	Cert *x509.Certificate
}

type CAList []*CA

// GenerateCA generates a CA key pair with the given filename
func GenerateCA(commonName, organizationalUnit string) (*CA, error) {
	cfg := &CertCfg{
		Subject:      pkix.Name{CommonName: commonName, OrganizationalUnit: []string{organizationalUnit}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		Validity:     ValidityTenYears,
		IsCA:         true,
	}

	key, crt, err := GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return nil, err
	}
	return &CA{Key: key, Cert: crt}, nil
}

func (c *CA) WriteTo(fileName string) error {
	if CertAndKeyExists(fileName) {
		return nil
	}
	certBytes := CertToPem(c.Cert)
	if err := ioutil.WriteFile(fileName+".crt", certBytes, 0644); err != nil {
		return err
	}

	keyBytes := PrivateKeyToPem(c.Key)
	if err := ioutil.WriteFile(fileName+".key", keyBytes, 0644); err != nil {
		return err
	}
	return nil
}

func (l CAList) WriteTo(fileName string) error {
	if CertExists(fileName) {
		return nil
	}
	var allBytes [][]byte
	for _, ca := range l {
		allBytes = append(allBytes, CertToPem(ca.Cert))
	}
	certBytes := bytes.Join(allBytes, []byte("\n"))
	if err := ioutil.WriteFile(fileName+".crt", certBytes, 0644); err != nil {
		return err
	}
	return nil
}
