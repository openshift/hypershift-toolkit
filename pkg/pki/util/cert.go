package util

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"io/ioutil"
	"net"
)

func GenerateCert(commonName, organization string, hostNames, addresses []string, ca *CA) (*Cert, error) {
	ipAddr := []net.IP{}
	for _, ip := range addresses {
		ipAddr = append(ipAddr, net.ParseIP(ip))
	}
	cfg := &CertCfg{
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{organization}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		Validity:     ValidityTenYears,
		DNSNames:     hostNames,
		IPAddresses:  ipAddr,
	}
	key, crt, err := GenerateSignedCertificate(ca.Key, ca.Cert, cfg)
	if err != nil {
		return nil, err
	}
	return &Cert{
		Parent: ca,
		Key:    key,
		Cert:   crt,
	}, nil
}

type Cert struct {
	Parent *CA
	Key    *rsa.PrivateKey
	Cert   *x509.Certificate
}

func (c *Cert) WriteTo(fileName string, appendParent bool) error {
	if CertExists(fileName) {
		return nil
	}
	keyBytes := PrivateKeyToPem(c.Key)
	if err := ioutil.WriteFile(fileName+".key", keyBytes, 0644); err != nil {
		return err
	}

	certBytes := CertToPem(c.Cert)
	if appendParent {
		certBytes = bytes.Join([][]byte{certBytes, CertToPem(c.Parent.Cert)}, []byte("\n"))
	}
	if err := ioutil.WriteFile(fileName+".crt", certBytes, 0644); err != nil {
		return err
	}
	return nil
}
