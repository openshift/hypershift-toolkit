package util

import (
	"os"
	"text/template"
)

func GenerateKubeconfig(serverAddress, commonName, organization string, ca *CA) (*Kubeconfig, error) {
	cert, err := GenerateCert(commonName, organization, nil, nil, ca)
	if err != nil {
		return nil, err
	}
	return &Kubeconfig{
		Cert:          cert,
		ServerAddress: serverAddress,
	}, nil
}

type Kubeconfig struct {
	*Cert
	ServerAddress string
}

var kubeConfigTemplate = template.Must(template.New("kubeconfig").Parse(`
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: {{ .CACert }}
    server: {{ .ServerAddress }}
  name: default
contexts:
- context:
    cluster: default
    user: admin
  name: default
current-context: default
kind: Config
preferences: {}
users:
- name: admin
  user:
    client-certificate-data: {{ .ClientCert }}
    client-key-data: {{ .ClientKey }}
`))

func (k *Kubeconfig) WriteTo(fileName string) error {
	if KubeconfigExists(fileName) {
		return nil
	}
	f, err := os.Create(fileName + ".kubeconfig")
	if err != nil {
		return err
	}
	caBytes := CertToPem(k.Parent.Cert)
	certBytes := CertToPem(k.Cert.Cert)
	keyBytes := PrivateKeyToPem(k.Cert.Key)
	params := map[string]string{
		"ServerAddress": k.ServerAddress,
		"CACert":        Base64(caBytes),
		"ClientCert":    Base64(certBytes),
		"ClientKey":     Base64(keyBytes),
	}
	if err := kubeConfigTemplate.Execute(f, params); err != nil {
		return err
	}
	return nil
}
