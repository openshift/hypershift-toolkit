package api

type ClusterParams struct {
	Namespace                           string             `json:"namespace"`
	ExternalAPIDNSName                  string             `json:"externalAPIDNSName"`
	ExternalAPIPort                     uint               `json:"externalAPIPort"`
	ExternalAPIIPAddress                string             `json:"externalAPIAddress"`
	ExternalOpenVPNDNSName              string             `json:"externalVPNDNSName"`
	ExternalOpenVPNPort                 uint               `json:"externalVPNPort"`
	ExternalOauthPort                   uint               `json:"externalOauthPort"`
	IdentityProviders                   string             `json:"identityProviders"`
	ServiceCIDR                         string             `json:"serviceCIDR"`
	NamedCerts                          []NamedCert        `json:"namedCerts,omitempty"`
	PodCIDR                             string             `json:"podCIDR"`
	ReleaseImage                        string             `json:"releaseImage"`
	APINodePort                         uint               `json:"apiNodePort"`
	IngressSubdomain                    string             `json:"ingressSubdomain"`
	OpenShiftAPIClusterIP               string             `json:"openshiftAPIClusterIP"`
	ImageRegistryHTTPSecret             string             `json:"imageRegistryHTTPSecret"`
	RouterNodePortHTTP                  string             `json:"routerNodePortHTTP"`
	RouterNodePortHTTPS                 string             `json:"routerNodePortHTTPS"`
	OpenVPNNodePort                     string             `json:"openVPNNodePort"`
	BaseDomain                          string             `json:"baseDomain"`
	NetworkType                         string             `json:"networkType"`
	Replicas                            string             `json:"replicas"`
	EtcdClientName                      string             `json:"etcdClientName"`
	OriginReleasePrefix                 string             `json:"originReleasePrefix"`
	OpenshiftAPIServerCABundle          string             `json:"openshiftAPIServerCABundle"`
	CloudProvider                       string             `json:"cloudProvider"`
	CVOSetupImage                       string             `json:"cvoSetupImage"`
	InternalAPIPort                     uint               `json:"internalAPIPort"`
	RouterServiceType                   string             `json:"routerServiceType"`
	KubeAPIServerResources              []ResourceRequests `json:"kubeAPIServerResources"`
	OpenshiftControllerManagerResources []ResourceRequests `json:"openshiftControllerManagerResources"`
	ClusterVersionOperatorResources     []ResourceRequests `json:"clusterVersionOperatorResources"`
	KubeControllerManagerResources      []ResourceRequests `json:"kubeControllerManagerResources"`
	OpenshiftAPIServerResources         []ResourceRequests `json:"openshiftAPIServerResources"`
	KubeSchedulerResources              []ResourceRequests `json:"kubeSchedulerResources"`
	CAOperatorResources                 []ResourceRequests `json:"cAOperatorResources"`
	OAuthServerResources                []ResourceRequests `json:"oAuthServerResources"`
	ClusterPolicyControllerResources    []ResourceRequests `json:"clusterPolicyControllerResources"`
	AutoApproverResources               []ResourceRequests `json:"autoApproverResources"`
	OpenVPNClientResources              []ResourceRequests `json:"openVPNClientResources"`
	OpenVPNServerResources              []ResourceRequests `json:"openVPNServerResources"`
}

type NamedCert struct {
	NamedCertPrefix string `json:"namedCertPrefix"`
	NamedCertDomain string `json:"namedCertDomain"`
}

type ResourceRequests struct {
	Limits   []Limits   `json:"limits"`
	Requests []Requests `json:"requests"`
}

type Limits struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type Requests struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}
