package api

type ClusterParams struct {
	Namespace               string      `json:"namespace"`
	ExternalAPIDNSName      string      `json:"externalAPIDNSName"`
	ExternalAPIPort         uint        `json:"externalAPIPort"`
	ExternalAPIIPAddress    string      `json:"externalAPIAddress"`
	ExternalOpenVPNDNSName  string      `json:"externalVPNDNSName"`
	ExternalOpenVPNPort     uint        `json:"externalVPNPort"`
	ExternalOauthPort       uint        `json:"externalOauthPort"`
	IdentityProviders       string      `json:identityProviders`
	ServiceCIDR             string      `json:"serviceCIDR"`
	NamedCerts              []NamedCert `json:"namedCerts,omitempty"`
	PodCIDR                 string      `json:"podCIDR"`
	ReleaseImage            string      `json:"releaseImage"`
	APINodePort             uint        `json:"apiNodePort"`
	IngressSubdomain        string      `json:"ingressSubdomain"`
	OpenShiftAPIClusterIP   string      `json:"openshiftAPIClusterIP"`
	ImageRegistryHTTPSecret string      `json:"imageRegistryHTTPSecret"`
	RouterNodePortHTTP      string      `json:"routerNodePortHTTP"`
	RouterNodePortHTTPS     string      `json:"routerNodePortHTTPS"`
	OpenVPNNodePort         string      `json:"openVPNNodePort"`
	BaseDomain              string      `json:"baseDomain"`
	NetworkType             string      `json:"networkType"`
	Replicas                string      `json:"replicas"`
	EtcdClientName          string      `json:"etcdClientName"`
	OriginReleasePrefix     string      `json:"originReleasePrefix"`
}

type NamedCert struct {
	NamedCertPrefix string `json:"namedCertPrefix"`
	NamedCertDomain string `json:"namedCertDomain"`
}
